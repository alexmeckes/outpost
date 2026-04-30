package outpost

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRelayForwardsRequestsAndStreamsResponses(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			if got := r.URL.Query().Get("scope"); got != "local" {
				http.Error(w, "missing query", http.StatusBadRequest)
				return
			}
			if got := r.Header.Get("Authorization"); got != "Bearer outpost-key" {
				http.Error(w, "missing authorization", http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"object":"list","data":[{"id":"llama3.2:1b"}]}`)
		case "/v1/chat/completions":
			body, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if !bytes.Contains(body, []byte(`"stream":true`)) {
				http.Error(w, "request body was not forwarded", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			flusher, ok := w.(http.Flusher)
			if !ok {
				http.Error(w, "missing flusher", http.StatusInternalServerError)
				return
			}
			io.WriteString(w, "data: one\n\n")
			flusher.Flush()
			io.WriteString(w, "data: [DONE]\n\n")
			flusher.Flush()
		default:
			http.NotFound(w, r)
		}
	}))
	defer target.Close()

	hub := NewRelayHub("dev-token")
	relay := httptest.NewServer(hub.Handler())
	defer relay.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- RunRelayClient(ctx, RelayClientOptions{
			RelayURL: relay.URL,
			Slug:     "demo",
			Target:   target.URL,
			Token:    "dev-token",
		})
	}()

	waitForRelayAgent(t, relay.URL, "demo", errCh)

	req, err := http.NewRequest(http.MethodGet, relay.URL+"/demo/v1/models?scope=local", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer outpost-key")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("models status = %d, body = %s", resp.StatusCode, data)
	}
	if !strings.Contains(string(data), `"id":"llama3.2:1b"`) {
		t.Fatalf("models body = %s", data)
	}

	streamReq, err := http.NewRequest(http.MethodPost, relay.URL+"/demo/v1/chat/completions", strings.NewReader(`{"model":"llama3.2:1b","stream":true}`))
	if err != nil {
		t.Fatal(err)
	}
	streamReq.Header.Set("Authorization", "Bearer outpost-key")
	streamReq.Header.Set("Content-Type", "application/json")

	streamResp, err := http.DefaultClient.Do(streamReq)
	if err != nil {
		t.Fatal(err)
	}
	streamData, err := io.ReadAll(streamResp.Body)
	streamResp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if streamResp.StatusCode != http.StatusOK {
		t.Fatalf("stream status = %d, body = %s", streamResp.StatusCode, streamData)
	}
	got := string(streamData)
	if !strings.Contains(got, "data: one\n\n") || !strings.Contains(got, "data: [DONE]\n\n") {
		t.Fatalf("stream body = %q", got)
	}
}

func TestRelayReservedSlugAndPublicAuth(t *testing.T) {
	targetHits := 0
	targetSawAuthorization := false
	targetSawRelayToken := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetHits++
		targetSawAuthorization = r.Header.Get("Authorization") == "Bearer outpost-key"
		targetSawRelayToken = r.Header.Get(DefaultRelayPublicAuthHeader) != ""
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"ok":true}`)
	}))
	defer target.Close()

	hub := NewRelayHubWithOptions(RelayHubOptions{
		AgentToken:       "agent-secret",
		PublicToken:      "public-secret",
		PublicAuthHeader: DefaultRelayPublicAuthHeader,
		Reservations: map[string]RelayReservation{
			"demo": {Slug: "demo", DeviceID: "od_testdevice"},
		},
		AllowUnreserved: false,
	})
	relay := httptest.NewServer(hub.Handler())
	defer relay.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- RunRelayClient(ctx, RelayClientOptions{
			RelayURL: relay.URL,
			Slug:     "demo",
			Target:   target.URL,
			Token:    "agent-secret",
			DeviceID: "od_testdevice",
		})
	}()
	waitForRelayAgent(t, relay.URL, "demo", errCh)

	unauthorizedReq, err := http.NewRequest(http.MethodGet, relay.URL+"/demo/v1/models", nil)
	if err != nil {
		t.Fatal(err)
	}
	unauthorizedReq.Header.Set("Authorization", "Bearer outpost-key")
	unauthorizedResp, err := http.DefaultClient.Do(unauthorizedReq)
	if err != nil {
		t.Fatal(err)
	}
	unauthorizedResp.Body.Close()
	if unauthorizedResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d, want %d", unauthorizedResp.StatusCode, http.StatusUnauthorized)
	}
	if targetHits != 0 {
		t.Fatalf("target was hit before relay-side auth passed: %d", targetHits)
	}

	authorizedReq, err := http.NewRequest(http.MethodGet, relay.URL+"/demo/v1/models", nil)
	if err != nil {
		t.Fatal(err)
	}
	authorizedReq.Header.Set("Authorization", "Bearer outpost-key")
	authorizedReq.Header.Set(DefaultRelayPublicAuthHeader, "Bearer public-secret")
	authorizedResp, err := http.DefaultClient.Do(authorizedReq)
	if err != nil {
		t.Fatal(err)
	}
	authorizedResp.Body.Close()
	if authorizedResp.StatusCode != http.StatusOK {
		t.Fatalf("authorized status = %d, want %d", authorizedResp.StatusCode, http.StatusOK)
	}
	if !targetSawAuthorization {
		t.Fatal("target did not receive Outpost Authorization header")
	}
	if targetSawRelayToken {
		t.Fatal("target received relay-side public auth header")
	}
}

func TestRelayUsesEndpointPublicToken(t *testing.T) {
	targetHits := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetHits++
		io.WriteString(w, `{"ok":true}`)
	}))
	defer target.Close()

	hub := NewRelayHubWithOptions(RelayHubOptions{
		AgentToken: "agent-secret",
		Reservations: map[string]RelayReservation{
			"demo": {
				Slug:             "demo",
				DeviceID:         "od_testdevice",
				PublicTokenHash:  HashToken("endpoint-secret"),
				PublicAuthHeader: DefaultRelayPublicAuthHeader,
			},
		},
		AllowUnreserved: false,
	})
	relay := httptest.NewServer(hub.Handler())
	defer relay.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- RunRelayClient(ctx, RelayClientOptions{
			RelayURL: relay.URL,
			Slug:     "demo",
			Target:   target.URL,
			Token:    "agent-secret",
			DeviceID: "od_testdevice",
		})
	}()
	waitForRelayAgent(t, relay.URL, "demo", errCh)

	req, err := http.NewRequest(http.MethodGet, relay.URL+"/demo/v1/models", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(DefaultRelayPublicAuthHeader, "wrong-secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong token status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}

	req, err = http.NewRequest(http.MethodGet, relay.URL+"/demo/v1/models", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(DefaultRelayPublicAuthHeader, "Bearer endpoint-secret")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("correct token status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if targetHits != 1 {
		t.Fatalf("target hits = %d, want 1", targetHits)
	}
}

func TestRelayHealthcheck(t *testing.T) {
	relay := httptest.NewServer(NewRelayHub("dev-token").Handler())
	defer relay.Close()

	resp, err := http.Get(relay.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d, body = %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `"ok":true`) {
		t.Fatalf("health body = %s", body)
	}
}

func TestRelayRejectsWrongReservedDevice(t *testing.T) {
	hub := NewRelayHubWithOptions(RelayHubOptions{
		AgentToken: "agent-secret",
		Reservations: map[string]RelayReservation{
			"demo": {Slug: "demo", DeviceID: "od_expected"},
		},
		AllowUnreserved: false,
	})
	relay := httptest.NewServer(hub.Handler())
	defer relay.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	err := RunRelayClient(ctx, RelayClientOptions{
		RelayURL: relay.URL,
		Slug:     "demo",
		Target:   relay.URL,
		Token:    "agent-secret",
		DeviceID: "od_other",
	})
	if !errors.Is(err, errPermanentRelayConnection) {
		t.Fatalf("error = %v, want permanent relay connection error", err)
	}
}

func TestEnsureRelayIdentityCreatesStableDeviceID(t *testing.T) {
	cfg := DefaultConfig()
	identity, created, err := cfg.EnsureRelayIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("identity was not created")
	}
	if !strings.HasPrefix(identity.DeviceID, "od_") {
		t.Fatalf("device ID = %q, want od_ prefix", identity.DeviceID)
	}

	again, created, err := cfg.EnsureRelayIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Fatal("identity should not be recreated")
	}
	if again.DeviceID != identity.DeviceID {
		t.Fatalf("device ID changed: %q != %q", again.DeviceID, identity.DeviceID)
	}
}

func TestNextRelayBackoffCapsAtMax(t *testing.T) {
	if got := nextRelayBackoff(500*time.Millisecond, 900*time.Millisecond); got != 900*time.Millisecond {
		t.Fatalf("backoff = %s, want 900ms", got)
	}
}

func waitForRelayAgent(t *testing.T, relayURL string, slug string, errCh <-chan error) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-errCh:
			t.Fatalf("relay client exited early: %v", err)
		default:
		}

		resp, err := http.Get(relayURL + "/")
		if err == nil {
			data, readErr := io.ReadAll(resp.Body)
			resp.Body.Close()
			if readErr != nil {
				t.Fatal(readErr)
			}
			if resp.StatusCode == http.StatusOK && strings.Contains(string(data), `"`+slug+`"`) {
				return
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("relay agent %q did not connect", slug)
}
