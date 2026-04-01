package auth

import (
	"context"
	"net/http"
	"strings"

	"github.com/alibaba/hiclaw/orchestrator/internal/httputil"
)

// Role constants.
const (
	RoleManager = "manager"
	RoleWorker  = "worker"
)

type contextKey string

const callerKey contextKey = "caller"

// CallerFromContext extracts the CallerIdentity from the request context.
func CallerFromContext(ctx context.Context) *CallerIdentity {
	if v := ctx.Value(callerKey); v != nil {
		return v.(*CallerIdentity)
	}
	return nil
}

// CallerKeyForTest returns the context key for injecting CallerIdentity in tests.
func CallerKeyForTest() contextKey {
	return callerKey
}

// Middleware provides HTTP authentication middleware.
type Middleware struct {
	keyStore *KeyStore
}

// NewMiddleware creates an auth Middleware.
func NewMiddleware(keyStore *KeyStore) *Middleware {
	return &Middleware{keyStore: keyStore}
}

// RequireManager returns middleware that only allows manager callers.
func (m *Middleware) RequireManager(next http.Handler) http.Handler {
	return m.requireRole(RoleManager, next)
}

// RequireWorker returns middleware that only allows worker callers.
func (m *Middleware) RequireWorker(next http.Handler) http.Handler {
	return m.requireRole(RoleWorker, next)
}

func (m *Middleware) requireRole(role string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !m.keyStore.AuthEnabled() {
			next.ServeHTTP(w, r)
			return
		}

		identity, ok := m.authenticate(r)
		if !ok {
			httputil.WriteError(w, http.StatusUnauthorized, "invalid or missing API key")
			return
		}
		if identity.Role != role {
			httputil.WriteError(w, http.StatusForbidden, role+" access required")
			return
		}

		ctx := context.WithValue(r.Context(), callerKey, identity)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (m *Middleware) authenticate(r *http.Request) (*CallerIdentity, bool) {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return nil, false
	}

	key := strings.TrimPrefix(authHeader, "Bearer ")
	if key == authHeader {
		return nil, false
	}

	return m.keyStore.ValidateKey(key)
}
