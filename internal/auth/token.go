package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const (
	Header      = "X-Llmirror-Token"
	GroupHeader = "X-Llmirror-Group"
)

// Middleware requires a shared fleet token when token is non-empty.
func Middleware(token string, next http.Handler) http.Handler {
	if token == "" {
		return next
	}
	want := []byte(token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get(Header)
		if got == "" {
			if a := r.Header.Get("Authorization"); strings.HasPrefix(a, "Bearer ") {
				got = strings.TrimPrefix(a, "Bearer ")
			}
		}
		if subtle.ConstantTimeCompare([]byte(got), want) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// GroupMiddleware requires matching fleet group when group is non-empty.
func GroupMiddleware(group string, next http.Handler) http.Handler {
	if group == "" {
		return next
	}
	want := []byte(group)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get(GroupHeader)
		if subtle.ConstantTimeCompare([]byte(got), want) != 1 {
			http.Error(w, "forbidden: fleet group mismatch", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RoundTripper injects fleet token and group on outbound peer requests.
type RoundTripper struct {
	Token string
	Group string
	Base  http.RoundTripper
}

func (t RoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}
	if t.Token == "" && t.Group == "" {
		return base.RoundTrip(req)
	}
	r2 := req.Clone(req.Context())
	if t.Token != "" {
		r2.Header.Set(Header, t.Token)
	}
	if t.Group != "" {
		r2.Header.Set(GroupHeader, t.Group)
	}
	return base.RoundTrip(r2)
}

// LoadToken resolves token from explicit value, file, or LLMIRROR_TOKEN / LLMIRROR_TOKEN_FILE.
func LoadToken(explicit, file string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if file == "" {
		file = os.Getenv("LLMIRROR_TOKEN_FILE")
	}
	if file != "" {
		data, err := os.ReadFile(file)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(data)), nil
	}
	return os.Getenv("LLMIRROR_TOKEN"), nil
}

// LoadGroup resolves group id from flag, file, or LLMIRROR_GROUP / LLMIRROR_GROUP_FILE.
func LoadGroup(explicit, file string) (string, error) {
	if explicit != "" {
		return strings.TrimSpace(explicit), nil
	}
	if file == "" {
		file = os.Getenv("LLMIRROR_GROUP_FILE")
	}
	if file != "" {
		data, err := os.ReadFile(file)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(data)), nil
	}
	if g := os.Getenv("LLMIRROR_GROUP"); g != "" {
		return strings.TrimSpace(g), nil
	}
	// Auto-use default config file when present.
	home, err := os.UserHomeDir()
	if err != nil {
		return "", nil
	}
	p := filepath.Join(home, ".config", "llmirror", "group")
	data, err := os.ReadFile(p)
	if err != nil {
		return "", nil
	}
	return strings.TrimSpace(string(data)), nil
}

// GenerateToken returns a 32-byte hex token suitable for fleet auth.
func GenerateToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// GenerateGroupID returns a short unique fleet group id (16 hex chars).
func GenerateGroupID() (string, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
