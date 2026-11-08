package proxy

import (
	"io"
	"net/http"
	"strings"
	"sync"
)

const acmeChallengePrefix = "/.well-known/acme-challenge/"

// ChallengeStore holds HTTP-01 key authorizations between the ACME client and
// the proxy listener.
type ChallengeStore struct {
	mu            sync.RWMutex
	tokens        map[string]string
	portListening func(int) bool
}

// NewChallengeStore creates an empty store.
func NewChallengeStore() *ChallengeStore {
	return &ChallengeStore{tokens: make(map[string]string)}
}

// PortListening reports whether the proxy listens on port.
func (c *ChallengeStore) PortListening(port int) bool {
	if c.portListening == nil {
		return false
	}
	return c.portListening(port)
}

// Put registers a token.
func (c *ChallengeStore) Put(token, keyAuth string) {
	c.mu.Lock()
	c.tokens[token] = keyAuth
	c.mu.Unlock()
}

// Delete removes a token.
func (c *ChallengeStore) Delete(token string) {
	c.mu.Lock()
	delete(c.tokens, token)
	c.mu.Unlock()
}

// Get returns a registered key authorization.
func (c *ChallengeStore) Get(token string) (string, bool) {
	c.mu.RLock()
	v, ok := c.tokens[token]
	c.mu.RUnlock()
	return v, ok
}

// Serve answers an ACME HTTP-01 request. It returns false when the path is not
// a challenge or the token is unknown, so normal routing can continue.
func (c *ChallengeStore) Serve(w http.ResponseWriter, r *http.Request) bool {
	if !strings.HasPrefix(r.URL.Path, acmeChallengePrefix) {
		return false
	}
	token := strings.TrimPrefix(r.URL.Path, acmeChallengePrefix)
	if token == "" || strings.Contains(token, "/") {
		return false
	}
	keyAuth, ok := c.Get(token)
	if !ok {
		return false
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, keyAuth)
	return true
}
