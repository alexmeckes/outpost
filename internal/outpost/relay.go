package outpost

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"nhooyr.io/websocket"
)

const (
	DefaultRelayListenAddr       = "127.0.0.1:8787"
	DefaultRelaySlug             = "demo"
	DefaultRelayTarget           = "http://" + DefaultListenAddr
	DefaultRelayToken            = "dev"
	DefaultRelayPublicAuthHeader = "X-Outpost-Relay-Token"
	maxRelayMessageBytes         = 16 << 20
)

type RelayServerOptions struct {
	ListenAddr       string
	Token            string
	PublicToken      string
	PublicAuthHeader string
	Reservations     map[string]RelayReservation
	AllowUnreserved  bool
}

type RelayClientOptions struct {
	RelayURL                string
	Slug                    string
	Target                  string
	Token                   string
	DeviceID                string
	Reconnect               bool
	InitialReconnectBackoff time.Duration
	MaxReconnectBackoff     time.Duration
}

type RelayReservation struct {
	Slug              string `json:"slug"`
	DeviceID          string `json:"device_id"`
	PublicTokenPrefix string `json:"public_token_prefix,omitempty"`
	PublicTokenHash   string `json:"public_token_hash,omitempty"`
	PublicAuthHeader  string `json:"public_auth_header,omitempty"`
}

type RelayHubOptions struct {
	AgentToken       string
	PublicToken      string
	PublicAuthHeader string
	Reservations     map[string]RelayReservation
	AllowUnreserved  bool
}

type relayMessage struct {
	Type   string      `json:"type"`
	ID     string      `json:"id,omitempty"`
	Method string      `json:"method,omitempty"`
	Path   string      `json:"path,omitempty"`
	Header http.Header `json:"header,omitempty"`
	Status int         `json:"status,omitempty"`
	Body   []byte      `json:"body,omitempty"`
	Error  string      `json:"error,omitempty"`
}

type RelayHub struct {
	agentToken       string
	publicTokenHash  string
	publicAuthHeader string
	reservations     map[string]RelayReservation
	allowUnreserved  bool

	mu     sync.RWMutex
	agents map[string]*relayAgent
}

type relayAgent struct {
	slug        string
	deviceID    string
	connectedAt time.Time
	conn        *websocket.Conn
	send        chan relayMessage
	done        chan struct{}

	mu      sync.Mutex
	pending map[string]chan relayMessage
	closed  bool
}

func RunRelayServer(ctx context.Context, opts RelayServerOptions) error {
	if opts.ListenAddr == "" {
		opts.ListenAddr = DefaultRelayListenAddr
	}
	if opts.PublicAuthHeader == "" {
		opts.PublicAuthHeader = DefaultRelayPublicAuthHeader
	}
	allowUnreserved := opts.AllowUnreserved
	if len(opts.Reservations) == 0 {
		allowUnreserved = true
	}

	hub := NewRelayHubWithOptions(RelayHubOptions{
		AgentToken:       opts.Token,
		PublicToken:      opts.PublicToken,
		PublicAuthHeader: opts.PublicAuthHeader,
		Reservations:     opts.Reservations,
		AllowUnreserved:  allowUnreserved,
	})
	server := &http.Server{
		Addr:              opts.ListenAddr,
		Handler:           hub.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		errCh <- server.Shutdown(shutdownCtx)
	}()

	fmt.Printf("Outpost relay listening on http://%s\n", opts.ListenAddr)
	fmt.Printf("Agent connect URL: http://%s/_outpost/connect?slug=%s\n", opts.ListenAddr, DefaultRelaySlug)
	fmt.Printf("Public dev URL: http://%s/%s/v1\n", opts.ListenAddr, DefaultRelaySlug)
	if len(opts.Reservations) > 0 {
		fmt.Printf("Reserved slugs: %s\n", strings.Join(hub.reservedSlugs(), ", "))
	}
	if opts.PublicToken != "" {
		fmt.Printf("Relay request auth: %s\n", opts.PublicAuthHeader)
	}

	err := server.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return <-errCh
	}
	return err
}

func NewRelayHub(token string) *RelayHub {
	return NewRelayHubWithOptions(RelayHubOptions{
		AgentToken:      token,
		AllowUnreserved: true,
	})
}

func NewRelayHubWithOptions(opts RelayHubOptions) *RelayHub {
	if opts.PublicAuthHeader == "" {
		opts.PublicAuthHeader = DefaultRelayPublicAuthHeader
	}
	reservations := normalizeRelayReservations(opts.Reservations)
	publicTokenHash := ""
	if opts.PublicToken != "" {
		publicTokenHash = HashToken(opts.PublicToken)
	}

	return &RelayHub{
		agentToken:       opts.AgentToken,
		publicTokenHash:  publicTokenHash,
		publicAuthHeader: http.CanonicalHeaderKey(opts.PublicAuthHeader),
		reservations:     reservations,
		allowUnreserved:  opts.AllowUnreserved,
		agents:           map[string]*relayAgent{},
	}
}

func (h *RelayHub) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", h.handleHealth)
	mux.HandleFunc("/_outpost/connect", h.handleConnect)
	mux.HandleFunc("/", h.handlePublic)
	return mux
}

func (h *RelayHub) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
}

func (h *RelayHub) handleConnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !h.authorized(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	slug := cleanRelaySlug(r.URL.Query().Get("slug"))
	if slug == "" {
		http.Error(w, "missing slug", http.StatusBadRequest)
		return
	}
	deviceID := cleanRelayDeviceID(r.URL.Query().Get("device_id"))
	if err := h.authorizeAgent(slug, deviceID); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: []string{"*"},
	})
	if err != nil {
		return
	}
	conn.SetReadLimit(maxRelayMessageBytes)

	agent := &relayAgent{
		slug:        slug,
		deviceID:    deviceID,
		connectedAt: time.Now().UTC(),
		conn:        conn,
		send:        make(chan relayMessage, 128),
		done:        make(chan struct{}),
		pending:     map[string]chan relayMessage{},
	}

	h.register(agent)
	defer func() {
		h.unregister(slug, agent)
		agent.close()
		conn.CloseNow()
	}()

	go agent.writeLoop(r.Context())
	agent.readLoop(r.Context())
}

func (h *RelayHub) handlePublic(w http.ResponseWriter, r *http.Request) {
	slug, upstreamPath, ok := splitRelayPath(r.URL)
	if !ok {
		h.writeStatus(w)
		return
	}
	if !h.authorizePublicRequest(slug, r) {
		writeRelayError(w, http.StatusUnauthorized, "relay authorization required")
		return
	}

	agent := h.agent(slug)
	if agent == nil {
		writeRelayError(w, http.StatusServiceUnavailable, fmt.Sprintf("no agent connected for slug %q", slug))
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeRelayError(w, http.StatusBadRequest, err.Error())
		return
	}

	id, err := newRelayID()
	if err != nil {
		writeRelayError(w, http.StatusInternalServerError, err.Error())
		return
	}

	responses, err := agent.registerPending(id)
	if err != nil {
		writeRelayError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	defer agent.unregisterPending(id)

	err = agent.sendMessage(r.Context(), relayMessage{
		Type:   "request",
		ID:     id,
		Method: r.Method,
		Path:   upstreamPath,
		Header: h.clonePublicRequestHeader(slug, r.Header),
		Body:   body,
	})
	if err != nil {
		writeRelayError(w, http.StatusBadGateway, err.Error())
		return
	}

	started := false
	for {
		select {
		case <-r.Context().Done():
			return
		case msg, ok := <-responses:
			if !ok {
				if !started {
					writeRelayError(w, http.StatusBadGateway, "agent disconnected")
				}
				return
			}
			switch msg.Type {
			case "response_start":
				copyRelayResponseHeaders(w.Header(), msg.Header)
				if msg.Status == 0 {
					msg.Status = http.StatusOK
				}
				w.WriteHeader(msg.Status)
				started = true
			case "response_chunk":
				if !started {
					w.WriteHeader(http.StatusOK)
					started = true
				}
				if len(msg.Body) > 0 {
					if _, err := w.Write(msg.Body); err != nil {
						return
					}
					if flusher, ok := w.(http.Flusher); ok {
						flusher.Flush()
					}
				}
			case "response_end":
				return
			case "error":
				if started {
					return
				}
				writeRelayError(w, http.StatusBadGateway, msg.Error)
				return
			}
		case <-time.After(60 * time.Second):
			if !started {
				writeRelayError(w, http.StatusGatewayTimeout, "relay request timed out")
			}
			return
		}
	}
}

func (h *RelayHub) authorized(r *http.Request) bool {
	if h.agentToken == "" {
		return true
	}
	auth := r.Header.Get("Authorization")
	return strings.EqualFold(auth, "Bearer "+h.agentToken)
}

func (h *RelayHub) authorizeAgent(slug string, deviceID string) error {
	reservation, ok := h.reservations[slug]
	if !ok {
		if h.allowUnreserved {
			return nil
		}
		return fmt.Errorf("slug %q is not reserved", slug)
	}
	if reservation.DeviceID == "" {
		return nil
	}
	if deviceID == "" {
		return fmt.Errorf("slug %q requires a device identity", slug)
	}
	if subtle.ConstantTimeCompare([]byte(deviceID), []byte(reservation.DeviceID)) != 1 {
		return fmt.Errorf("slug %q is reserved for another device", slug)
	}
	return nil
}

func (h *RelayHub) authorizePublicRequest(slug string, r *http.Request) bool {
	tokenHash := h.publicTokenHash
	authHeader := h.publicAuthHeader
	if reservation, ok := h.reservations[slug]; ok {
		if reservation.PublicTokenHash != "" {
			tokenHash = reservation.PublicTokenHash
		}
		if reservation.PublicAuthHeader != "" {
			authHeader = reservation.PublicAuthHeader
		}
	}
	if tokenHash == "" {
		return true
	}
	token := relayTokenFromHeader(r.Header.Get(authHeader))
	if token == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(HashToken(token)), []byte(tokenHash)) == 1
}

func (h *RelayHub) clonePublicRequestHeader(slug string, src http.Header) http.Header {
	dst := cloneRelayRequestHeader(src)
	if h.publicAuthHeader != "" {
		dst.Del(h.publicAuthHeader)
	}
	if reservation, ok := h.reservations[slug]; ok && reservation.PublicAuthHeader != "" {
		dst.Del(reservation.PublicAuthHeader)
	}
	return dst
}

func (h *RelayHub) writeStatus(w http.ResponseWriter) {
	h.mu.RLock()
	slugs := make([]string, 0, len(h.agents))
	devices := map[string]string{}
	for slug := range h.agents {
		slugs = append(slugs, slug)
		devices[slug] = h.agents[slug].deviceID
	}
	h.mu.RUnlock()

	endpoints := map[string]any{}
	for slug, reservation := range h.reservations {
		_, connected := devices[slug]
		endpoints[slug] = map[string]any{
			"device_id":           reservation.DeviceID,
			"connected":           connected,
			"public_token_prefix": reservation.PublicTokenPrefix,
			"public_auth_header":  reservation.PublicAuthHeader,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":           true,
		"agents":       slugs,
		"devices":      devices,
		"reservations": h.reservedSlugs(),
		"endpoints":    endpoints,
	})
}

func (h *RelayHub) reservedSlugs() []string {
	slugs := make([]string, 0, len(h.reservations))
	for slug := range h.reservations {
		slugs = append(slugs, slug)
	}
	return slugs
}

func (h *RelayHub) register(agent *relayAgent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if previous := h.agents[agent.slug]; previous != nil {
		previous.close()
	}
	h.agents[agent.slug] = agent
}

func (h *RelayHub) unregister(slug string, agent *relayAgent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.agents[slug] == agent {
		delete(h.agents, slug)
	}
}

func (h *RelayHub) agent(slug string) *relayAgent {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.agents[slug]
}

func (a *relayAgent) readLoop(ctx context.Context) {
	for {
		typ, data, err := a.conn.Read(ctx)
		if err != nil {
			return
		}
		if typ != websocket.MessageText {
			continue
		}

		var msg relayMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		a.dispatch(msg)
	}
}

func (a *relayAgent) writeLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-a.done:
			return
		case msg := <-a.send:
			if err := writeRelayMessage(ctx, a.conn, msg); err != nil {
				return
			}
		}
	}
}

func (a *relayAgent) sendMessage(ctx context.Context, msg relayMessage) error {
	a.mu.Lock()
	closed := a.closed
	a.mu.Unlock()
	if closed {
		return errors.New("agent disconnected")
	}

	select {
	case <-a.done:
		return errors.New("agent disconnected")
	default:
	}

	select {
	case a.send <- msg:
		select {
		case <-a.done:
			return errors.New("agent disconnected")
		default:
		}
		return nil
	case <-a.done:
		return errors.New("agent disconnected")
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(5 * time.Second):
		return errors.New("agent send queue blocked")
	}
}

func (a *relayAgent) registerPending(id string) (<-chan relayMessage, error) {
	ch := make(chan relayMessage, 128)
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return nil, errors.New("agent disconnected")
	}
	a.pending[id] = ch
	return ch, nil
}

func (a *relayAgent) unregisterPending(id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.pending, id)
}

func (a *relayAgent) dispatch(msg relayMessage) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if ch := a.pending[msg.ID]; ch != nil {
		select {
		case ch <- msg:
		default:
		}
	}
}

func (a *relayAgent) close() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.closed {
		return
	}
	a.closed = true
	close(a.done)
	for id, ch := range a.pending {
		close(ch)
		delete(a.pending, id)
	}
}

func RunRelayClient(ctx context.Context, opts RelayClientOptions) error {
	opts.applyDefaults()

	if !opts.Reconnect {
		err := runRelayClientOnce(ctx, opts)
		if ctx.Err() != nil {
			return nil
		}
		return err
	}

	backoff := opts.InitialReconnectBackoff
	for {
		err := runRelayClientOnce(ctx, opts)
		if ctx.Err() != nil {
			return nil
		}
		if errors.Is(err, errPermanentRelayConnection) {
			return err
		}
		fmt.Printf("Relay disconnected: %v; reconnecting in %s\n", err, backoff)
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
		backoff = nextRelayBackoff(backoff, opts.MaxReconnectBackoff)
	}
}

var errPermanentRelayConnection = errors.New("permanent relay connection error")

func runRelayClientOnce(ctx context.Context, opts RelayClientOptions) error {
	targetBase, err := url.Parse(strings.TrimRight(opts.Target, "/"))
	if err != nil {
		return err
	}

	connectURL, err := relayConnectURL(opts.RelayURL, opts.Slug, opts.DeviceID)
	if err != nil {
		return err
	}

	header := http.Header{}
	if opts.Token != "" {
		header.Set("Authorization", "Bearer "+opts.Token)
	}

	conn, resp, err := websocket.Dial(ctx, connectURL, &websocket.DialOptions{
		HTTPHeader: header,
	})
	if err != nil {
		if resp != nil && resp.StatusCode >= 400 && resp.StatusCode < 500 {
			return fmt.Errorf("%w: relay returned %s", errPermanentRelayConnection, resp.Status)
		}
		return err
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	conn.SetReadLimit(maxRelayMessageBytes)

	if opts.DeviceID != "" {
		fmt.Printf("Connected relay slug %q as device %s to %s\n", opts.Slug, opts.DeviceID, opts.Target)
	} else {
		fmt.Printf("Connected relay slug %q to %s\n", opts.Slug, opts.Target)
	}
	fmt.Printf("Public dev base URL: %s/%s/v1\n", strings.TrimRight(opts.RelayURL, "/"), opts.Slug)

	client := &http.Client{Timeout: 0}
	for {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		if typ != websocket.MessageText {
			continue
		}

		var msg relayMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		if msg.Type != "request" {
			continue
		}
		go handleRelayedRequest(ctx, conn, client, targetBase, msg)
	}
}

func (opts *RelayClientOptions) applyDefaults() {
	if opts.RelayURL == "" {
		opts.RelayURL = "http://" + DefaultRelayListenAddr
	}
	if opts.Slug == "" {
		opts.Slug = DefaultRelaySlug
	}
	if opts.Target == "" {
		opts.Target = DefaultRelayTarget
	}
	opts.DeviceID = cleanRelayDeviceID(opts.DeviceID)
	if opts.InitialReconnectBackoff <= 0 {
		opts.InitialReconnectBackoff = 500 * time.Millisecond
	}
	if opts.MaxReconnectBackoff <= 0 {
		opts.MaxReconnectBackoff = 30 * time.Second
	}
	if opts.MaxReconnectBackoff < opts.InitialReconnectBackoff {
		opts.MaxReconnectBackoff = opts.InitialReconnectBackoff
	}
}

func handleRelayedRequest(ctx context.Context, conn *websocket.Conn, client *http.Client, targetBase *url.URL, msg relayMessage) {
	targetURL, err := relayTargetURL(targetBase, msg.Path)
	if err != nil {
		_ = writeRelayMessage(ctx, conn, relayMessage{Type: "error", ID: msg.ID, Error: err.Error()})
		return
	}

	req, err := http.NewRequestWithContext(ctx, msg.Method, targetURL, bytes.NewReader(msg.Body))
	if err != nil {
		_ = writeRelayMessage(ctx, conn, relayMessage{Type: "error", ID: msg.ID, Error: err.Error()})
		return
	}
	req.Header = cloneRelayRequestHeader(msg.Header)

	resp, err := client.Do(req)
	if err != nil {
		_ = writeRelayMessage(ctx, conn, relayMessage{Type: "error", ID: msg.ID, Error: err.Error()})
		return
	}
	defer resp.Body.Close()

	err = writeRelayMessage(ctx, conn, relayMessage{
		Type:   "response_start",
		ID:     msg.ID,
		Status: resp.StatusCode,
		Header: cloneRelayResponseHeader(resp.Header),
	})
	if err != nil {
		return
	}

	buf := make([]byte, 32*1024)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if err := writeRelayMessage(ctx, conn, relayMessage{
				Type: "response_chunk",
				ID:   msg.ID,
				Body: append([]byte(nil), buf[:n]...),
			}); err != nil {
				return
			}
		}
		if readErr != nil {
			if readErr != io.EOF {
				_ = writeRelayMessage(ctx, conn, relayMessage{Type: "error", ID: msg.ID, Error: readErr.Error()})
				return
			}
			break
		}
	}

	_ = writeRelayMessage(ctx, conn, relayMessage{Type: "response_end", ID: msg.ID})
}

func writeRelayMessage(ctx context.Context, conn *websocket.Conn, msg relayMessage) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	writeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return conn.Write(writeCtx, websocket.MessageText, data)
}

func nextRelayBackoff(current, max time.Duration) time.Duration {
	if current <= 0 {
		current = 500 * time.Millisecond
	}
	next := current * 2
	if next > max {
		return max
	}
	return next
}

func relayConnectURL(relayURL string, slug string, deviceID string) (string, error) {
	base, err := url.Parse(strings.TrimRight(relayURL, "/"))
	if err != nil {
		return "", err
	}
	if base.Scheme == "" {
		base.Scheme = "http"
	}
	base.Path = "/_outpost/connect"
	q := base.Query()
	q.Set("slug", cleanRelaySlug(slug))
	if deviceID != "" {
		q.Set("device_id", cleanRelayDeviceID(deviceID))
	}
	base.RawQuery = q.Encode()
	return base.String(), nil
}

func relayTargetURL(base *url.URL, path string) (string, error) {
	if path == "" {
		path = "/"
	}
	rel, err := url.Parse(path)
	if err != nil {
		return "", err
	}
	if rel.IsAbs() {
		return "", fmt.Errorf("refusing absolute relayed URL %q", path)
	}
	return base.ResolveReference(rel).String(), nil
}

func splitRelayPath(u *url.URL) (slug string, upstreamPath string, ok bool) {
	clean := strings.TrimPrefix(u.EscapedPath(), "/")
	if clean == "" || strings.HasPrefix(clean, "_outpost/") {
		return "", "", false
	}

	parts := strings.SplitN(clean, "/", 2)
	slug = cleanRelaySlug(parts[0])
	if slug == "" {
		return "", "", false
	}
	upstreamPath = "/"
	if len(parts) == 2 && parts[1] != "" {
		upstreamPath += parts[1]
	}
	if u.RawQuery != "" {
		upstreamPath += "?" + u.RawQuery
	}
	return slug, upstreamPath, true
}

func cleanRelaySlug(slug string) string {
	slug = strings.TrimSpace(strings.ToLower(slug))
	var b strings.Builder
	for _, r := range slug {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-':
			b.WriteRune(r)
		}
	}
	return strings.Trim(b.String(), "-")
}

func NormalizeRelaySlug(slug string) string {
	return cleanRelaySlug(slug)
}

func cleanRelayDeviceID(deviceID string) string {
	deviceID = strings.TrimSpace(strings.ToLower(deviceID))
	var b strings.Builder
	for _, r := range deviceID {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_':
			b.WriteRune(r)
		}
	}
	return strings.Trim(b.String(), "-_")
}

func NormalizeRelayDeviceID(deviceID string) string {
	return cleanRelayDeviceID(deviceID)
}

func normalizeRelayReservations(src map[string]RelayReservation) map[string]RelayReservation {
	dst := map[string]RelayReservation{}
	for slug, reservation := range src {
		cleanSlug := cleanRelaySlug(slug)
		if cleanSlug == "" {
			cleanSlug = cleanRelaySlug(reservation.Slug)
		}
		if cleanSlug == "" {
			continue
		}
		reservation.Slug = cleanSlug
		reservation.DeviceID = cleanRelayDeviceID(reservation.DeviceID)
		if reservation.PublicAuthHeader == "" {
			reservation.PublicAuthHeader = DefaultRelayPublicAuthHeader
		}
		dst[cleanSlug] = reservation
	}
	return dst
}

func relayTokenFromHeader(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= len("bearer ") && strings.EqualFold(value[:len("bearer ")], "bearer ") {
		value = strings.TrimSpace(value[len("bearer "):])
	}
	return value
}

func newRelayID() (string, error) {
	data, err := randomBytes(16)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(data), nil
}

func cloneRelayRequestHeader(src http.Header) http.Header {
	dst := http.Header{}
	for key, values := range src {
		if skipRelayRequestHeader(key) {
			continue
		}
		for _, value := range values {
			dst.Add(key, value)
		}
	}
	return dst
}

func cloneRelayResponseHeader(src http.Header) http.Header {
	dst := http.Header{}
	copyRelayResponseHeaders(dst, src)
	return dst
}

func copyRelayResponseHeaders(dst, src http.Header) {
	for key, values := range src {
		if skipResponseHeader(key) {
			continue
		}
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func skipRelayRequestHeader(key string) bool {
	switch strings.ToLower(key) {
	case "connection", "keep-alive", "proxy-authenticate", "proxy-authorization", "te", "trailer", "transfer-encoding", "upgrade":
		return true
	default:
		return false
	}
}

func writeRelayError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": message,
	})
}
