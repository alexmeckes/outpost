package outpost

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Server struct {
	cfg     *Config
	logger  *RequestLogger
	client  *http.Client
	limiter *RateLimiter
}

type contextKey string

const apiKeyContextKey contextKey = "api-key"

func NewServer(cfg *Config, logger *RequestLogger) *Server {
	cfg.applyDefaults()
	return &Server{
		cfg:    cfg,
		logger: logger,
		client: &http.Client{
			Timeout: 0,
		},
		limiter: NewRateLimiter(),
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealth)
	mux.HandleFunc("/v1/models", s.withAuth(s.handleModels))
	mux.HandleFunc("/v1/chat/completions", s.withAuth(s.handleOpenAIProxy))
	mux.HandleFunc("/v1/completions", s.withAuth(s.handleOpenAIProxy))
	mux.HandleFunc("/v1/embeddings", s.withAuth(s.handleOpenAIProxy))
	return mux
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	io.WriteString(w, `{"ok":true}`+"\n")
}

func (s *Server) withAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key, ok := s.cfg.Authenticate(r.Header.Get("Authorization"))
		if !ok {
			_ = s.logger.Write(RequestLog{
				Time:     time.Now().UTC(),
				Method:   r.Method,
				Path:     r.URL.Path,
				Backend:  s.cfg.Backend.BaseURL,
				Status:   http.StatusUnauthorized,
				Duration: "0s",
				Error:    "missing or invalid bearer token",
			})
			writeJSONError(w, http.StatusUnauthorized, "missing or invalid bearer token")
			return
		}
		if !s.limiter.Allow(key) {
			_ = s.logger.Write(RequestLog{
				Time:     time.Now().UTC(),
				Method:   r.Method,
				Path:     r.URL.Path,
				KeyID:    key.ID,
				Backend:  s.cfg.Backend.BaseURL,
				Status:   http.StatusTooManyRequests,
				Duration: "0s",
				Error:    "rate limit exceeded",
			})
			writeJSONError(w, http.StatusTooManyRequests, "rate limit exceeded")
			return
		}
		ctx := context.WithValue(r.Context(), apiKeyContextKey, key)
		next(w, r.WithContext(ctx))
	}
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	key := apiKeyFromContext(r.Context())
	status, bytesWritten, logErr := 0, int64(0), ""
	defer func() {
		_ = s.logger.Write(RequestLog{
			Time:     time.Now().UTC(),
			Method:   r.Method,
			Path:     r.URL.Path,
			KeyID:    key.ID,
			Backend:  s.cfg.Backend.BaseURL,
			Status:   status,
			Bytes:    bytesWritten,
			Duration: time.Since(start).String(),
			Error:    logErr,
		})
	}()

	if r.Method != http.MethodGet {
		status = http.StatusMethodNotAllowed
		writeJSONError(w, status, "method not allowed")
		return
	}

	if len(s.cfg.ModelAliases) == 0 {
		var err error
		status, bytesWritten, err = s.forward(w, r, nil, "")
		if err != nil {
			logErr = err.Error()
		}
		return
	}

	body, backendStatus, err := s.fetchBackendModels(r)
	if err != nil {
		status = http.StatusBadGateway
		logErr = err.Error()
		writeJSONError(w, status, err.Error())
		return
	}
	if backendStatus < 200 || backendStatus >= 300 {
		status = backendStatus
		w.WriteHeader(backendStatus)
		n, _ := w.Write(body)
		bytesWritten = int64(n)
		return
	}

	augmented, err := s.addAliasModels(body)
	if err != nil {
		status = http.StatusBadGateway
		logErr = err.Error()
		writeJSONError(w, status, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	status = http.StatusOK
	w.WriteHeader(status)
	n, _ := w.Write(augmented)
	bytesWritten = int64(n)
}

func (s *Server) handleOpenAIProxy(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	key := apiKeyFromContext(r.Context())
	status, bytesWritten, model, logErr := 0, int64(0), "", ""
	defer func() {
		_ = s.logger.Write(RequestLog{
			Time:     time.Now().UTC(),
			Method:   r.Method,
			Path:     r.URL.Path,
			KeyID:    key.ID,
			Model:    model,
			Backend:  s.cfg.Backend.BaseURL,
			Status:   status,
			Bytes:    bytesWritten,
			Duration: time.Since(start).String(),
			Error:    logErr,
		})
	}()

	if r.Method != http.MethodPost {
		status = http.StatusMethodNotAllowed
		writeJSONError(w, status, "method not allowed")
		return
	}

	body, resolvedModel, err := s.rewriteModel(r.Body)
	if err != nil {
		status = http.StatusBadRequest
		logErr = err.Error()
		writeJSONError(w, status, err.Error())
		return
	}
	model = resolvedModel

	status, bytesWritten, err = s.forward(w, r, body, r.URL.Path)
	if err != nil {
		logErr = err.Error()
	}
}

func (s *Server) rewriteModel(body io.Reader) ([]byte, string, error) {
	data, err := io.ReadAll(body)
	if err != nil {
		return nil, "", err
	}

	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, "", fmt.Errorf("request body must be JSON: %w", err)
	}

	model, _ := payload["model"].(string)
	if model == "" {
		return data, "", nil
	}
	if target, ok := s.cfg.ModelAliases[model]; ok && target != "" {
		payload["model"] = target
		rewritten, err := json.Marshal(payload)
		if err != nil {
			return nil, model, err
		}
		return rewritten, model + " -> " + target, nil
	}
	return data, model, nil
}

func (s *Server) forward(w http.ResponseWriter, inbound *http.Request, body []byte, overridePath string) (int, int64, error) {
	target, err := s.backendURL(inbound.URL.Path)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, err.Error())
		return http.StatusBadGateway, 0, err
	}
	if overridePath != "" {
		target, err = s.backendURL(overridePath)
		if err != nil {
			writeJSONError(w, http.StatusBadGateway, err.Error())
			return http.StatusBadGateway, 0, err
		}
	}

	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	} else {
		reader = inbound.Body
	}

	req, err := http.NewRequestWithContext(inbound.Context(), inbound.Method, target, reader)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, err.Error())
		return http.StatusBadGateway, 0, err
	}
	copyRequestHeaders(req.Header, inbound.Header)
	if s.cfg.Backend.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+s.cfg.Backend.APIKey)
	}
	if body != nil {
		req.ContentLength = int64(len(body))
	}

	resp, err := s.client.Do(req)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, err.Error())
		return http.StatusBadGateway, 0, err
	}
	defer resp.Body.Close()

	if inbound.Method == http.MethodGet && inbound.URL.Path == "/v1/models" && resp.StatusCode >= 200 && resp.StatusCode < 300 {
		normalized, err := normalizeOpenAIModels(resp.Body)
		if err != nil {
			writeJSONError(w, http.StatusBadGateway, err.Error())
			return http.StatusBadGateway, 0, err
		}
		copyResponseHeaders(w.Header(), resp.Header)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		n, err := w.Write(normalized)
		return resp.StatusCode, int64(n), err
	}

	copyResponseHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	bytesWritten, copyErr := copyAndFlush(w, resp.Body)
	return resp.StatusCode, bytesWritten, copyErr
}

func (s *Server) fetchBackendModels(inbound *http.Request) ([]byte, int, error) {
	target, err := s.backendURL("/v1/models")
	if err != nil {
		return nil, 0, err
	}
	req, err := http.NewRequestWithContext(inbound.Context(), http.MethodGet, target, nil)
	if err != nil {
		return nil, 0, err
	}
	copyRequestHeaders(req.Header, inbound.Header)
	if s.cfg.Backend.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+s.cfg.Backend.APIKey)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	return body, resp.StatusCode, err
}

func (s *Server) addAliasModels(body []byte) ([]byte, error) {
	var payload struct {
		Object string           `json:"object"`
		Data   []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	if payload.Object == "" {
		payload.Object = "list"
	}
	if payload.Data == nil {
		payload.Data = []map[string]any{}
	}

	existing := map[string]bool{}
	for _, model := range payload.Data {
		if id, _ := model["id"].(string); id != "" {
			existing[id] = true
		}
	}

	for alias := range s.cfg.ModelAliases {
		if alias == "" || existing[alias] {
			continue
		}
		payload.Data = append(payload.Data, map[string]any{
			"id":       alias,
			"object":   "model",
			"created":  time.Now().Unix(),
			"owned_by": "outpost",
		})
	}
	return json.Marshal(payload)
}

func normalizeOpenAIModels(body io.Reader) ([]byte, error) {
	data, err := io.ReadAll(body)
	if err != nil {
		return nil, err
	}

	var payload struct {
		Object string           `json:"object"`
		Data   []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return data, nil
	}
	if payload.Object == "" {
		payload.Object = "list"
	}
	if payload.Data == nil {
		payload.Data = []map[string]any{}
		return json.Marshal(payload)
	}
	return data, nil
}

func (s *Server) backendURL(path string) (string, error) {
	base, err := url.Parse(s.cfg.Backend.BaseURL)
	if err != nil {
		return "", err
	}
	return base.ResolveReference(&url.URL{Path: path}).String(), nil
}

func apiKeyFromContext(ctx context.Context) APIKey {
	key, _ := ctx.Value(apiKeyContextKey).(APIKey)
	return key
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"message": message,
			"type":    "outpost_error",
		},
	})
}

func copyRequestHeaders(dst, src http.Header) {
	for key, values := range src {
		if skipRequestHeader(key) {
			continue
		}
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func copyResponseHeaders(dst, src http.Header) {
	for key, values := range src {
		if skipResponseHeader(key) {
			continue
		}
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func skipRequestHeader(key string) bool {
	switch strings.ToLower(key) {
	case "authorization", "connection", "keep-alive", "proxy-authenticate", "proxy-authorization", "te", "trailer", "transfer-encoding", "upgrade":
		return true
	default:
		return false
	}
}

func skipResponseHeader(key string) bool {
	switch strings.ToLower(key) {
	case "connection", "content-length", "keep-alive", "proxy-authenticate", "proxy-authorization", "te", "trailer", "transfer-encoding", "upgrade":
		return true
	default:
		return false
	}
}

func copyAndFlush(dst io.Writer, src io.Reader) (int64, error) {
	buf := make([]byte, 32*1024)
	var written int64
	flusher, _ := dst.(http.Flusher)

	for {
		nr, er := src.Read(buf)
		if nr > 0 {
			nw, ew := dst.Write(buf[:nr])
			if nw > 0 {
				written += int64(nw)
			}
			if flusher != nil {
				flusher.Flush()
			}
			if ew != nil {
				return written, ew
			}
			if nr != nw {
				return written, io.ErrShortWrite
			}
		}
		if er != nil {
			if er == io.EOF {
				return written, nil
			}
			return written, er
		}
	}
}
