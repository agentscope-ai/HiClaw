package auth

import (
	"context"
	"fmt"

	v1beta1 "github.com/hiclaw/hiclaw-controller/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// IdentityEnricher resolves additional identity fields (role, team) from
// the backing store. Called after authentication to fill the full CallerIdentity.
type IdentityEnricher interface {
	EnrichIdentity(ctx context.Context, identity *CallerIdentity) error
}

// CREnricher enriches CallerIdentity by looking up Worker CR annotations.
type CREnricher struct {
	client    client.Client
	namespace string
}

// NewCREnricher creates an enricher that reads Worker CR annotations.
func NewCREnricher(c client.Client, namespace string) *CREnricher {
	return &CREnricher{client: c, namespace: namespace}
}

func (e *CREnricher) EnrichIdentity(ctx context.Context, identity *CallerIdentity) error {
	if identity == nil {
		return nil
	}

	// Admin and manager identities are fully resolved from SA name alone.
	if identity.Role == RoleAdmin || identity.Role == RoleManager {
		return nil
	}

	// Worker / team-leader: look up the Worker CR for role and team. The
	// derived labels hiclaw.io/role and hiclaw.io/team are maintained by
	// WorkerReconciler.syncWorkerLabels as the source of truth — they
	// always mirror spec.role / spec.teamRef after the first reconcile.
	var worker v1beta1.Worker
	key := client.ObjectKey{Name: identity.Username, Namespace: e.namespace}
	if err := e.client.Get(ctx, key, &worker); err != nil {
		return fmt.Errorf("enrich identity: get worker %q: %w", identity.Username, err)
	}

	if role := worker.Labels[v1beta1.LabelRole]; role == v1beta1.WorkerRoleTeamLeader {
		identity.Role = RoleTeamLeader
	}
	if team := worker.Labels[v1beta1.LabelTeam]; team != "" {
		identity.Team = team
	}

	return nil
}
