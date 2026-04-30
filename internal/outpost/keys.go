package outpost

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"time"
)

type APIKey struct {
	ID                string     `json:"id"`
	Name              string     `json:"name"`
	Prefix            string     `json:"prefix"`
	Hash              string     `json:"hash"`
	CreatedAt         time.Time  `json:"created_at"`
	RevokedAt         *time.Time `json:"revoked_at,omitempty"`
	RequestsPerMinute int        `json:"requests_per_minute"`
}

func NewAPIKey(name string, requestsPerMinute int) (APIKey, string, error) {
	idBytes, err := randomBytes(6)
	if err != nil {
		return APIKey{}, "", err
	}
	secretBytes, err := randomBytes(32)
	if err != nil {
		return APIKey{}, "", err
	}

	id := hex.EncodeToString(idBytes)
	token := "op_" + base64.RawURLEncoding.EncodeToString(secretBytes)
	prefix := token
	if len(prefix) > 12 {
		prefix = prefix[:12]
	}
	if requestsPerMinute < 0 {
		requestsPerMinute = 0
	}

	return APIKey{
		ID:                id,
		Name:              name,
		Prefix:            prefix,
		Hash:              HashToken(token),
		CreatedAt:         time.Now().UTC(),
		RequestsPerMinute: requestsPerMinute,
	}, token, nil
}

func (cfg *Config) Authenticate(authHeader string) (APIKey, bool) {
	const prefix = "bearer "
	if len(authHeader) < len(prefix) || strings.ToLower(authHeader[:len(prefix)]) != prefix {
		return APIKey{}, false
	}
	token := strings.TrimSpace(authHeader[len(prefix):])
	if token == "" {
		return APIKey{}, false
	}

	hash := HashToken(token)
	for _, key := range cfg.APIKeys {
		if key.RevokedAt != nil {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(hash), []byte(key.Hash)) == 1 {
			return key, true
		}
	}
	return APIKey{}, false
}

func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func randomBytes(n int) ([]byte, error) {
	data := make([]byte, n)
	_, err := rand.Read(data)
	return data, err
}
