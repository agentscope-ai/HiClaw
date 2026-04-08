package auth

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
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

// SA name prefixes used to derive entity identity.
const (
	SAWorkerPrefix  = "hiclaw-worker-"
	SAManagerName   = "hiclaw-manager"
	SAAdminName     = "hiclaw-admin"
	DefaultAudience = "hiclaw-controller"
)

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
type TokenReviewAuthenticator struct {
	client   kubernetes.Interface
	audience string

	cacheMu  sync.RWMutex
	cache    map[[32]byte]cachedResult
	cacheTTL time.Duration
}

type cachedResult struct {
	identity *CallerIdentity
	expiry   time.Time
}

// NewTokenReviewAuthenticator creates an authenticator backed by the K8s TokenReview API.
// audience is the expected token audience (typically "hiclaw-controller").
func NewTokenReviewAuthenticator(client kubernetes.Interface, audience string) *TokenReviewAuthenticator {
	if audience == "" {
		audience = DefaultAudience
	}
	return &TokenReviewAuthenticator{
		client:   client,
		audience: audience,
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

	identity, err := ParseSAUsername(result.Status.User.Username)
	if err != nil {
		return nil, err
	}

	a.putInCache(key, identity)
	return identity, nil
}

// ParseSAUsername extracts identity from a K8s SA username.
// Format: "system:serviceaccount:{namespace}:{sa-name}"
func ParseSAUsername(username string) (*CallerIdentity, error) {
	const saPrefix = "system:serviceaccount:"
	if !strings.HasPrefix(username, saPrefix) {
		return nil, fmt.Errorf("unexpected username format: %q", username)
	}

	parts := strings.SplitN(username[len(saPrefix):], ":", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("cannot parse SA from username: %q", username)
	}
	saName := parts[1]

	switch {
	case saName == SAAdminName:
		return &CallerIdentity{Role: RoleAdmin, Username: "admin"}, nil
	case saName == SAManagerName:
		return &CallerIdentity{Role: RoleManager, Username: "manager"}, nil
	case strings.HasPrefix(saName, SAWorkerPrefix):
		name := saName[len(SAWorkerPrefix):]
		return &CallerIdentity{Role: RoleWorker, Username: name, WorkerName: name}, nil
	default:
		return nil, fmt.Errorf("unrecognized SA name pattern: %q", saName)
	}
}

// SAName returns the K8s ServiceAccount name for an entity.
func SAName(role, name string) string {
	switch role {
	case RoleAdmin:
		return SAAdminName
	case RoleManager:
		return SAManagerName
	default:
		return SAWorkerPrefix + name
	}
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
