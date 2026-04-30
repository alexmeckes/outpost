package outpost

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAuthRequired(t *testing.T) {
	cfg, _ := testConfig(t)
	server := httptest.NewServer(NewServer(cfg, NewRequestLogger("")).Handler())
	defer server.Close()

	resp, err := http.Get(server.URL + "/v1/models")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusUnauthorized)
	}
}

func TestModelsProxyStripsClientAuthorization(t *testing.T) {
	backendSawAuthorization := false
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("path = %s, want /v1/models", r.URL.Path)
		}
		backendSawAuthorization = r.Header.Get("Authorization") != ""
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"object":"list","data":[{"id":"llama3.2","object":"model","created":1,"owned_by":"library"}]}`)
	}))
	defer backend.Close()

	cfg, token := testConfig(t)
	cfg.Backend.BaseURL = backend.URL
	server := httptest.NewServer(NewServer(cfg, NewRequestLogger("")).Handler())
	defer server.Close()

	req, err := http.NewRequest(http.MethodGet, server.URL+"/v1/models", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
	if backendSawAuthorization {
		t.Fatal("backend received client Authorization header")
	}
}

func TestModelsProxyHonorsBackendBaseURLWithV1(t *testing.T) {
	backendPath := ""
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backendPath = r.URL.Path
		if r.URL.Path != "/v1/models" {
			http.Error(w, "bad path", http.StatusTeapot)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"object":"list","data":[{"id":"local-model"}]}`)
	}))
	defer backend.Close()

	cfg, token := testConfig(t)
	cfg.Backend.Type = "openai-compatible"
	cfg.Backend.BaseURL = backend.URL + "/v1"
	server := httptest.NewServer(NewServer(cfg, NewRequestLogger("")).Handler())
	defer server.Close()

	req, err := http.NewRequest(http.MethodGet, server.URL+"/v1/models", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body = %s", resp.StatusCode, data)
	}
	if backendPath != "/v1/models" {
		t.Fatalf("backend path = %s, want /v1/models", backendPath)
	}
}

func TestModelsNormalizesNullData(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"object":"list","data":null}`)
	}))
	defer backend.Close()

	cfg, token := testConfig(t)
	cfg.Backend.BaseURL = backend.URL
	server := httptest.NewServer(NewServer(cfg, NewRequestLogger("")).Handler())
	defer server.Close()

	req, err := http.NewRequest(http.MethodGet, server.URL+"/v1/models", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(data), `{"object":"list","data":[]}`; got != want {
		t.Fatalf("body = %s, want %s", got, want)
	}
}

func TestChatCompletionsRewritesAliasAndStreams(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path = %s, want /v1/chat/completions", r.URL.Path)
		}

		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload["model"] != "qwen2.5-coder:32b" {
			t.Fatalf("model = %v, want qwen2.5-coder:32b", payload["model"])
		}

		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("test response writer does not flush")
		}
		io.WriteString(w, "data: first\n\n")
		flusher.Flush()
		io.WriteString(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer backend.Close()

	cfg, token := testConfig(t)
	cfg.Backend.BaseURL = backend.URL
	cfg.ModelAliases["gpt-4-turbo"] = "qwen2.5-coder:32b"
	server := httptest.NewServer(NewServer(cfg, NewRequestLogger("")).Handler())
	defer server.Close()

	body := bytes.NewBufferString(`{"model":"gpt-4-turbo","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
	req, err := http.NewRequest(http.MethodPost, server.URL+"/v1/chat/completions", body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, body = %s", resp.StatusCode, data)
	}
	got := string(data)
	if !strings.Contains(got, "data: first\n\n") || !strings.Contains(got, "data: [DONE]\n\n") {
		t.Fatalf("stream body = %q", got)
	}
}

func TestRateLimit(t *testing.T) {
	cfg, token := testConfig(t)
	cfg.APIKeys[0].RequestsPerMinute = 1
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"object":"list","data":[]}`)
	}))
	defer backend.Close()
	cfg.Backend.BaseURL = backend.URL

	server := httptest.NewServer(NewServer(cfg, NewRequestLogger("")).Handler())
	defer server.Close()

	for i, want := range []int{http.StatusOK, http.StatusTooManyRequests} {
		req, err := http.NewRequest(http.MethodGet, server.URL+"/v1/models", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Fatalf("request %d status = %d, want %d", i+1, resp.StatusCode, want)
		}
	}
}

func TestCheckBackendUsesOllamaVersion(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/version":
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"version":"0.12.4"}`)
		case "/v1/models":
			t.Errorf("unexpected fallback to %s", r.URL.Path)
			http.NotFound(w, r)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer backend.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	status, err := CheckBackend(ctx, BackendConfig{
		Type:    "ollama",
		BaseURL: backend.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	if status != "reachable, version 0.12.4" {
		t.Fatalf("status = %q", status)
	}
}

func TestCheckBackendLMStudioUsesModelsEndpoint(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"object":"list","data":[{"id":"loaded-a"},{"id":"loaded-b"}]}`)
		case "/api/version":
			t.Errorf("unexpected Ollama version check for LM Studio")
			http.NotFound(w, r)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer backend.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	status, err := CheckBackend(ctx, BackendConfig{
		Type:    "lmstudio",
		BaseURL: backend.URL + "/v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if status != "reachable, 2 models" {
		t.Fatalf("status = %q", status)
	}
}

func TestCheckBackendFallsBackToOpenAICompatibleModels(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/version":
			http.NotFound(w, r)
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"object":"list","data":[{"id":"loaded-model"}]}`)
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer backend.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	status, err := CheckBackend(ctx, BackendConfig{
		Type:    "ollama",
		BaseURL: backend.URL + "/v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if status != "reachable, 1 model" {
		t.Fatalf("status = %q", status)
	}
}

func testConfig(t *testing.T) (*Config, string) {
	t.Helper()
	key, token, err := NewAPIKey("test", DefaultRequestsPerMinute)
	if err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.APIKeys = []APIKey{key}
	cfg.LogPath = ""
	return cfg, token
}
