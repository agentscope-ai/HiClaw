package service

import (
	"context"
	"fmt"

	v1beta1 "github.com/hiclaw/hiclaw-controller/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// WorkerObservation is a minimal projection of a Worker used by the team
// reconciler to compute Team.status without relying on the full CR shape.
type WorkerObservation struct {
	Name         string
	Role         string
	TeamRef      string
	MatrixUserID string
	Ready        bool
	Namespace    string
}

// HumanObservation is a minimal projection of a Human used by the team
// reconciler to compute Team.status.admins.
type HumanObservation struct {
	Name         string
	MatrixUserID string
	TeamRole     string // admin | member (filled from the matched teamAccess entry)
}

// ObserverConfig holds configuration for constructing an Observer.
type ObserverConfig struct {
	Client client.Client
}

// Observer implements the TeamObserver interface declared in interfaces.go.
// It is a thin wrapper over the controller-runtime cached client that adapts
// CR lists into minimal projections consumable by the Team reconciler.
type Observer struct {
	client client.Client
}

// NewObserver constructs an Observer using the supplied client.
func NewObserver(cfg ObserverConfig) *Observer {
	return &Observer{client: cfg.Client}
}

// ListTeamMembers returns every Worker that claims membership in the named
// Team via spec.teamRef (regardless of role). The label hiclaw.io/team is
// maintained by WorkerReconciler to mirror spec.teamRef, so the query is
// an indexed list on the label rather than a full-scan filter.
// Namespace-scoped: callers should set ctx namespace explicitly via the
// client; Observer does not enforce its own namespace filter.
func (o *Observer) ListTeamMembers(ctx context.Context, teamName string) ([]WorkerObservation, error) {
	if o.client == nil {
		return nil, fmt.Errorf("observer: nil client")
	}
	var list v1beta1.WorkerList
	if err := o.client.List(ctx, &list, client.MatchingLabels{v1beta1.LabelTeam: teamName}); err != nil {
		return nil, fmt.Errorf("list workers by team=%s: %w", teamName, err)
	}
	out := make([]WorkerObservation, 0, len(list.Items))
	for i := range list.Items {
		w := &list.Items[i]
		// Defensive: drop any Worker whose spec.teamRef does not actually
		// match the requested team (label may lag if reconciler hasn't run).
		if w.Spec.TeamRef != teamName {
			continue
		}
		out = append(out, WorkerObservation{
			Name:         w.Name,
			Role:         w.Spec.EffectiveRole(),
			TeamRef:      w.Spec.TeamRef,
			MatrixUserID: w.Status.MatrixUserID,
			Ready:        isWorkerReady(w),
			Namespace:    w.Namespace,
		})
	}
	return out, nil
}

// ListTeamAdmins returns every Human that declares teamAccess[].role=admin
// for the named Team. SuperAdmin Humans are NOT considered team admins by
// default; they are handled via Manager/Worker reconciler allow-list
// computation. If a Human has multiple entries for the same team (webhook
// rejects this), the first matching admin entry wins.
func (o *Observer) ListTeamAdmins(ctx context.Context, teamName string) ([]HumanObservation, error) {
	if o.client == nil {
		return nil, fmt.Errorf("observer: nil client")
	}
	var list v1beta1.HumanList
	if err := o.client.List(ctx, &list); err != nil {
		return nil, fmt.Errorf("list humans: %w", err)
	}
	out := make([]HumanObservation, 0)
	for i := range list.Items {
		h := &list.Items[i]
		for _, entry := range h.Spec.TeamAccess {
			if entry.Team != teamName {
				continue
			}
			if entry.Role != v1beta1.TeamAccessRoleAdmin {
				continue
			}
			out = append(out, HumanObservation{
				Name:         h.Name,
				MatrixUserID: h.Status.MatrixUserID,
				TeamRole:     entry.Role,
			})
			break
		}
	}
	return out, nil
}

// isWorkerReady collapses the Worker readiness decision into a single
// boolean: the Worker has a provisioned Matrix identity and is either
// Running or has an explicit Ready=True condition.
func isWorkerReady(w *v1beta1.Worker) bool {
	if w == nil || w.Status.MatrixUserID == "" {
		return false
	}
	switch w.Status.Phase {
	case v1beta1.StateRunning, "Ready", "Active":
		return true
	}
	for _, c := range w.Status.Conditions {
		if c.Type == v1beta1.ConditionReady && string(c.Status) == "True" {
			return true
		}
	}
	return false
}
