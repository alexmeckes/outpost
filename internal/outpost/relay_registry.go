package outpost

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	defaultRelayRegistryFile = "relay_endpoints.json"
)

type RelayRegistry struct {
	Endpoints []RelayEndpoint `json:"endpoints"`
}

type RelayEndpoint struct {
	Slug              string     `json:"slug"`
	DeviceID          string     `json:"device_id"`
	PublicTokenPrefix string     `json:"public_token_prefix,omitempty"`
	PublicTokenHash   string     `json:"public_token_hash,omitempty"`
	PublicAuthHeader  string     `json:"public_auth_header,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	RevokedAt         *time.Time `json:"revoked_at,omitempty"`
}

type RelayRegistryLoadResult struct {
	Registry *RelayRegistry
	Path     string
	Created  bool
}

type RelayEndpointCreateOptions struct {
	Slug             string
	DeviceID         string
	PublicToken      string
	PublicAuthHeader string
	Replace          bool
}

func ResolveRelayRegistryPath(path string) (string, error) {
	if path == "" {
		path = os.Getenv("OUTPOST_RELAY_REGISTRY")
	}
	if path != "" {
		return filepath.Abs(path)
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "outpost", defaultRelayRegistryFile), nil
}

func LoadRelayRegistry(path string) (*RelayRegistryLoadResult, error) {
	resolved, err := ResolveRelayRegistryPath(path)
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(resolved)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &RelayRegistryLoadResult{Registry: &RelayRegistry{}, Path: resolved}, nil
		}
		return nil, err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return &RelayRegistryLoadResult{Registry: &RelayRegistry{}, Path: resolved}, nil
	}

	var registry RelayRegistry
	if err := json.Unmarshal(data, &registry); err != nil {
		return nil, fmt.Errorf("parse relay registry %s: %w", resolved, err)
	}
	registry.applyDefaults()
	return &RelayRegistryLoadResult{Registry: &registry, Path: resolved}, nil
}

func SaveRelayRegistry(path string, registry *RelayRegistry) error {
	resolved, err := ResolveRelayRegistryPath(path)
	if err != nil {
		return err
	}
	registry.applyDefaults()
	if err := os.MkdirAll(filepath.Dir(resolved), defaultConfigDirectoryPerm); err != nil {
		return err
	}
	data, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(resolved, data, defaultConfigFilePerm)
}

func (r *RelayRegistry) applyDefaults() {
	for i := range r.Endpoints {
		endpoint := &r.Endpoints[i]
		endpoint.Slug = cleanRelaySlug(endpoint.Slug)
		endpoint.DeviceID = cleanRelayDeviceID(endpoint.DeviceID)
		if endpoint.PublicAuthHeader == "" {
			endpoint.PublicAuthHeader = DefaultRelayPublicAuthHeader
		}
	}
}

func (r *RelayRegistry) ActiveEndpoints() []RelayEndpoint {
	r.applyDefaults()
	endpoints := make([]RelayEndpoint, 0, len(r.Endpoints))
	for _, endpoint := range r.Endpoints {
		if endpoint.RevokedAt == nil {
			endpoints = append(endpoints, endpoint)
		}
	}
	sort.Slice(endpoints, func(i, j int) bool {
		return endpoints[i].Slug < endpoints[j].Slug
	})
	return endpoints
}

func (r *RelayRegistry) Reservations() map[string]RelayReservation {
	reservations := map[string]RelayReservation{}
	for _, endpoint := range r.ActiveEndpoints() {
		reservations[endpoint.Slug] = RelayReservation{
			Slug:              endpoint.Slug,
			DeviceID:          endpoint.DeviceID,
			PublicTokenPrefix: endpoint.PublicTokenPrefix,
			PublicTokenHash:   endpoint.PublicTokenHash,
			PublicAuthHeader:  endpoint.PublicAuthHeader,
		}
	}
	return reservations
}

func (r *RelayRegistry) CreateEndpoint(opts RelayEndpointCreateOptions) (RelayEndpoint, string, error) {
	slug := cleanRelaySlug(opts.Slug)
	if slug == "" {
		return RelayEndpoint{}, "", errors.New("endpoint slug is empty")
	}
	deviceID := cleanRelayDeviceID(opts.DeviceID)
	if deviceID == "" {
		return RelayEndpoint{}, "", errors.New("endpoint device ID is empty")
	}
	publicAuthHeader := strings.TrimSpace(opts.PublicAuthHeader)
	if publicAuthHeader == "" {
		publicAuthHeader = DefaultRelayPublicAuthHeader
	}

	if _, ok := r.ActiveEndpoint(slug); ok && !opts.Replace {
		return RelayEndpoint{}, "", fmt.Errorf("endpoint %q already exists; use --replace to update it", slug)
	}
	if opts.Replace {
		r.RevokeEndpoint(slug)
	}

	publicToken, publicTokenHash, publicTokenPrefix, err := prepareRelayPublicToken(opts.PublicToken)
	if err != nil {
		return RelayEndpoint{}, "", err
	}

	endpoint := RelayEndpoint{
		Slug:              slug,
		DeviceID:          deviceID,
		PublicTokenPrefix: publicTokenPrefix,
		PublicTokenHash:   publicTokenHash,
		PublicAuthHeader:  publicAuthHeader,
		CreatedAt:         time.Now().UTC(),
	}
	r.Endpoints = append(r.Endpoints, endpoint)
	return endpoint, publicToken, nil
}

func (r *RelayRegistry) ActiveEndpoint(slug string) (RelayEndpoint, bool) {
	slug = cleanRelaySlug(slug)
	for _, endpoint := range r.ActiveEndpoints() {
		if endpoint.Slug == slug {
			return endpoint, true
		}
	}
	return RelayEndpoint{}, false
}

func (r *RelayRegistry) RevokeEndpoint(slug string) bool {
	slug = cleanRelaySlug(slug)
	now := time.Now().UTC()
	revoked := false
	for i := range r.Endpoints {
		endpoint := &r.Endpoints[i]
		if endpoint.RevokedAt != nil {
			continue
		}
		if endpoint.Slug == slug {
			endpoint.RevokedAt = &now
			revoked = true
		}
	}
	return revoked
}

func NewRelayPublicToken() (string, error) {
	data, err := randomBytes(32)
	if err != nil {
		return "", err
	}
	return "orp_" + base64.RawURLEncoding.EncodeToString(data), nil
}

func NewRelayAgentToken() (string, error) {
	data, err := randomBytes(32)
	if err != nil {
		return "", err
	}
	return "ort_" + base64.RawURLEncoding.EncodeToString(data), nil
}

func prepareRelayPublicToken(value string) (token string, hash string, prefix string, err error) {
	value = strings.TrimSpace(value)
	if value == "" || strings.EqualFold(value, "auto") {
		token, err = NewRelayPublicToken()
		if err != nil {
			return "", "", "", err
		}
	} else if strings.EqualFold(value, "none") {
		return "", "", "", nil
	} else {
		token = value
	}
	prefix = token
	if len(prefix) > 12 {
		prefix = prefix[:12]
	}
	return token, HashToken(token), prefix, nil
}
