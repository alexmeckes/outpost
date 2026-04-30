package outpost

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultListenAddr          = "127.0.0.1:7341"
	DefaultLANListenAddr       = "0.0.0.0:7341"
	DefaultBackendURL          = "http://127.0.0.1:11434"
	DefaultRequestsPerMinute   = 120
	defaultConfigFilePerm      = 0o600
	defaultConfigDirectoryPerm = 0o700
)

type Config struct {
	ListenAddr   string            `json:"listen_addr"`
	Backend      BackendConfig     `json:"backend"`
	APIKeys      []APIKey          `json:"api_keys"`
	ModelAliases map[string]string `json:"model_aliases"`
	LogPath      string            `json:"log_path"`
	Relay        RelayConfig       `json:"relay"`
}

type BackendConfig struct {
	Type    string `json:"type"`
	BaseURL string `json:"base_url"`
	APIKey  string `json:"api_key,omitempty"`
}

type RelayConfig struct {
	DeviceID  string     `json:"device_id,omitempty"`
	CreatedAt *time.Time `json:"created_at,omitempty"`
}

type RelayIdentity struct {
	DeviceID  string
	CreatedAt time.Time
}

type LoadResult struct {
	Config     *Config
	Path       string
	Created    bool
	InitialKey string
}

func LoadOrCreateConfig(path string) (*LoadResult, error) {
	resolved, err := ResolveConfigPath(path)
	if err != nil {
		return nil, err
	}

	cfg, err := LoadConfig(resolved)
	if err == nil {
		return &LoadResult{Config: cfg, Path: resolved}, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	cfg = DefaultConfig()
	key, token, err := NewAPIKey("default", DefaultRequestsPerMinute)
	if err != nil {
		return nil, err
	}
	cfg.APIKeys = append(cfg.APIKeys, key)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	if status, err := CheckBackend(ctx, cfg.Backend); err == nil && status != "" {
		// The default backend is alive. Keeping this branch explicit makes later
		// multi-runtime detection easy to add without changing first-run behavior.
		cfg.Backend.BaseURL = DefaultBackendURL
	}

	if err := SaveConfig(resolved, cfg); err != nil {
		return nil, err
	}
	return &LoadResult{Config: cfg, Path: resolved, Created: true, InitialKey: token}, nil
}

func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	cfg.applyDefaults()
	return &cfg, nil
}

func SaveConfig(path string, cfg *Config) error {
	cfg.applyDefaults()
	if err := os.MkdirAll(filepath.Dir(path), defaultConfigDirectoryPerm); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, defaultConfigFilePerm)
}

func ResolveConfigPath(path string) (string, error) {
	if path == "" {
		path = os.Getenv("OUTPOST_CONFIG")
	}
	if path != "" {
		return filepath.Abs(path)
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "outpost", "config.json"), nil
}

func DefaultConfig() *Config {
	return &Config{
		ListenAddr: DefaultListenAddr,
		Backend: BackendConfig{
			Type:    "ollama",
			BaseURL: DefaultBackendURL,
		},
		ModelAliases: map[string]string{},
		LogPath:      defaultLogPath(),
	}
}

func (cfg *Config) applyDefaults() {
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = DefaultListenAddr
	}
	if cfg.Backend.Type == "" {
		cfg.Backend.Type = "ollama"
	}
	if cfg.Backend.BaseURL == "" {
		cfg.Backend.BaseURL = DefaultBackendURL
	}
	cfg.Backend.BaseURL = strings.TrimRight(cfg.Backend.BaseURL, "/")
	if cfg.ModelAliases == nil {
		cfg.ModelAliases = map[string]string{}
	}
	if cfg.LogPath == "" {
		cfg.LogPath = defaultLogPath()
	}
}

func (cfg *Config) ActiveKeys() []APIKey {
	keys := make([]APIKey, 0, len(cfg.APIKeys))
	for _, key := range cfg.APIKeys {
		if key.RevokedAt == nil {
			keys = append(keys, key)
		}
	}
	return keys
}

func (cfg *Config) RevokeKey(match string) bool {
	now := time.Now().UTC()
	for i := range cfg.APIKeys {
		key := &cfg.APIKeys[i]
		if key.RevokedAt != nil {
			continue
		}
		if key.ID == match || key.Prefix == match || strings.HasPrefix(key.ID, match) || strings.HasPrefix(key.Prefix, match) {
			key.RevokedAt = &now
			return true
		}
	}
	return false
}

func (cfg *Config) EnsureRelayIdentity() (RelayIdentity, bool, error) {
	if cfg.Relay.DeviceID != "" {
		createdAt := time.Now().UTC()
		if cfg.Relay.CreatedAt != nil {
			createdAt = *cfg.Relay.CreatedAt
		}
		return RelayIdentity{DeviceID: cfg.Relay.DeviceID, CreatedAt: createdAt}, false, nil
	}

	deviceID, err := NewRelayDeviceID()
	if err != nil {
		return RelayIdentity{}, false, err
	}
	createdAt := time.Now().UTC()
	cfg.Relay.DeviceID = deviceID
	cfg.Relay.CreatedAt = &createdAt
	return RelayIdentity{DeviceID: deviceID, CreatedAt: createdAt}, true, nil
}

func NewRelayDeviceID() (string, error) {
	data, err := randomBytes(12)
	if err != nil {
		return "", err
	}
	return "od_" + hex.EncodeToString(data), nil
}

func CheckBackend(ctx context.Context, backend BackendConfig) (string, error) {
	baseURL, err := url.Parse(strings.TrimRight(backend.BaseURL, "/"))
	if err != nil {
		return "", err
	}

	if strings.EqualFold(backend.Type, "ollama") || backend.Type == "" {
		if status, err := checkOllamaBackend(ctx, baseURL, backend); err == nil {
			return status, nil
		}
	}

	return checkOpenAICompatibleBackend(ctx, baseURL, backend)
}

func checkOllamaBackend(ctx context.Context, baseURL *url.URL, backend BackendConfig) (string, error) {
	checkURL := baseURL.ResolveReference(&url.URL{Path: "/api/version"}).String()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, checkURL, nil)
	if err != nil {
		return "", err
	}
	if backend.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+backend.APIKey)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("%s returned %s", checkURL, resp.Status)
	}

	var body struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "reachable", nil
	}
	if body.Version == "" {
		return "reachable", nil
	}
	return "reachable, version " + body.Version, nil
}

func checkOpenAICompatibleBackend(ctx context.Context, baseURL *url.URL, backend BackendConfig) (string, error) {
	checkURL := baseURL.ResolveReference(&url.URL{Path: "/v1/models"}).String()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, checkURL, nil)
	if err != nil {
		return "", err
	}
	if backend.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+backend.APIKey)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("%s returned %s", checkURL, resp.Status)
	}

	var body struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "reachable", nil
	}
	count := len(body.Data)
	if count == 0 {
		return "reachable", nil
	}
	return "reachable, " + strconv.Itoa(count) + " model" + plural(count), nil
}

func plural(count int) string {
	if count == 1 {
		return ""
	}
	return "s"
}

func defaultLogPath() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return filepath.Join(os.TempDir(), "outpost", "requests.jsonl")
	}
	return filepath.Join(dir, "outpost", "requests.jsonl")
}
