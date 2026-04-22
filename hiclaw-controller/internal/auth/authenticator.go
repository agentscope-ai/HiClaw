package auth

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sync"
	"time"

	authenticationv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// Role constants.
const (
	RoleAdmin      = "admin"
	RoleManager    = "manager"
	RoleTeamLeader = "team-leader"
	RoleWorker     = "worker"
)

// DefaultAudience is the SA token audience used by TokenReview when a caller
// does not specify one explicitly.
const DefaultAudience = "hiclaw-controller"

// CallerIdentity represents the authenticated caller.
type CallerIdentity struct {
	Role       string // admin | manager | team-leader | worker
	Username   string // canonical name (worker name, "manager", or "admin")
	Team       string // team name (filled by Enricher, empty for standalone)
	WorkerName string // equals Username when Role is worker or team-leader
}

// Authenticator validates a bearer token and returns a basic identity.
type Authenticator interface {
	Authenticate(ctx context.Context, token string) (*CallerIdentity, error)
}

// TokenReviewAuthenticator validates tokens via the K8s TokenReview API.
//
// TODO(auth): The cache has no max size or periodic eviction. Expired entries are
// ignored on read but not deleted. This is fine for the expected scale (entries ≈
// active workers), but a periodic sweep or LRU cap should be added if the number
// of unique tokens grows significantly.
type TokenReviewAuthenticator struct {
	client   kubernetes.Interface
	audience string
	prefix   ResourcePrefix

	cacheMu  sync.RWMutex
	cache    map[[32]byte]cachedResult
	cacheTTL time.Duration
}

type cachedResult struct {
	identity *CallerIdentity
	expiry   time.Time
}

// NewTokenReviewAuthenticator creates an authenticator backed by the K8s
// TokenReview API. audience is the expected token audience (typically
// "hiclaw-controller"); prefix is the tenant resource prefix used to parse
// SA usernames back into CallerIdentity.
func NewTokenReviewAuthenticator(client kubernetes.Interface, audience string, prefix ResourcePrefix) *TokenReviewAuthenticator {
	if audience == "" {
		audience = DefaultAudience
	}
	return &TokenReviewAuthenticator{
		client:   client,
		audience: audience,
		prefix:   prefix.Or(DefaultResourcePrefix),
		cache:    make(map[[32]byte]cachedResult),
		cacheTTL: 5 * time.Minute,
	}
}

func (a *TokenReviewAuthenticator) Authenticate(ctx context.Context, token string) (*CallerIdentity, error) {
	if token == "" {
		return nil, fmt.Errorf("empty token")
	}

	key := sha256.Sum256([]byte(token))

	if id := a.getFromCache(key); id != nil {
		return id, nil
	}

	review := &authenticationv1.TokenReview{
		Spec: authenticationv1.TokenReviewSpec{
			Token:     token,
			Audiences: []string{a.audience},
		},
	}

	result, err := a.client.AuthenticationV1().TokenReviews().Create(ctx, review, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("token review request failed: %w", err)
	}

	if !result.Status.Authenticated {
		return nil, fmt.Errorf("token not authenticated: %s", result.Status.Error)
	}

	identity, err := a.prefix.ParseSAUsername(result.Status.User.Username)
	if err != nil {
		return nil, err
	}

	a.putInCache(key, identity)
	return identity, nil
}

func (a *TokenReviewAuthenticator) getFromCache(key [32]byte) *CallerIdentity {
	a.cacheMu.RLock()
	defer a.cacheMu.RUnlock()
	if entry, ok := a.cache[key]; ok && time.Now().Before(entry.expiry) {
		cp := *entry.identity
		return &cp
	}
	return nil
}

func (a *TokenReviewAuthenticator) putInCache(key [32]byte, identity *CallerIdentity) {
	a.cacheMu.Lock()
	defer a.cacheMu.Unlock()
	cp := *identity
	a.cache[key] = cachedResult{
		identity: &cp,
		expiry:   time.Now().Add(a.cacheTTL),
	}
}

// InvalidateCache removes all cached entries. Useful after SA deletion.
func (a *TokenReviewAuthenticator) InvalidateCache() {
	a.cacheMu.Lock()
	defer a.cacheMu.Unlock()
	a.cache = make(map[[32]byte]cachedResult)
}
