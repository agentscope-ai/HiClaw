package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	v1beta1 "github.com/hiclaw/hiclaw-controller/api/v1beta1"
	"github.com/hiclaw/hiclaw-controller/internal/httputil"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// BundleHandler exposes aggregate create / delete endpoints that expand into
// multiple CR operations server-side. Unlike the raw CR endpoints, bundle
// operations always return 207 Multi-Status so partial failures are visible
// to the caller without short-circuiting the whole request.
type BundleHandler struct {
	client    client.Client
	namespace string
}

func NewBundleHandler(c client.Client, namespace string) *BundleHandler {
	return &BundleHandler{client: c, namespace: namespace}
}

// CreateTeamBundle handles POST /api/v1/bundles/team. It dry-runs all
// validators first and rejects the request with 400 if any structural
// invariant fails. Then it creates Team -> Leader Worker -> Member Workers
// and patches Admin Humans' teamAccess lists, recording per-resource
// outcomes. Missing Admin Humans surface as warnings (non-fatal) so the
// caller can create / fix them later without re-running the bundle.
func (h *BundleHandler) CreateTeamBundle(w http.ResponseWriter, r *http.Request) {
	var req TeamBundleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if req.Name == "" {
		httputil.WriteError(w, http.StatusBadRequest, "name is required")
		return
	}
	if req.Leader.Name == "" {
		httputil.WriteError(w, http.StatusBadRequest, "leader.name is required")
		return
	}

	team := buildTeamFromBundle(&req, h.namespace)
	leader := buildLeaderFromBundle(&req, h.namespace)
	members := buildMembersFromBundle(&req, h.namespace)

	items := make([]BundleResultItem, 0, 3+len(members)+len(req.Admins))

	// Phase 1: create Team. Downstream Worker creates carry a teamRef
	// pointing to this Team, so short-circuit if the Team itself fails —
	// any orphan Workers would fail validation (or worse, succeed and be
	// orphaned until the user retries).
	if err := h.client.Create(r.Context(), team); err != nil {
		items = append(items, teamItemFromError(team.Name, "create", err))
		httputil.WriteJSON(w, http.StatusMultiStatus, BundleResponse{Items: items})
		return
	}
	items = append(items, BundleResultItem{Kind: "team", Name: team.Name, Status: "created"})

	// Phase 2: create Leader Worker. Continue on failure so the caller
	// sees every problem in a single response.
	if err := h.client.Create(r.Context(), leader); err != nil {
		items = append(items, workerItemFromError(leader.Name, err))
	} else {
		items = append(items, BundleResultItem{Kind: "worker", Name: leader.Name, Status: "created"})
	}

	// Phase 3: create Member Workers.
	for i := range members {
		if err := h.client.Create(r.Context(), members[i]); err != nil {
			items = append(items, workerItemFromError(members[i].Name, err))
			continue
		}
		items = append(items, BundleResultItem{Kind: "worker", Name: members[i].Name, Status: "created"})
	}

	// Phase 4: patch Admin Humans. Missing Humans become warnings; the
	// admin relationship can be established retroactively when the Human
	// CR is later applied, because the HumanReconciler observes teamAccess.
	for _, adminName := range req.Admins {
		items = append(items, h.attachAdmin(r.Context(), adminName, req.Name))
	}

	httputil.WriteJSON(w, http.StatusMultiStatus, BundleResponse{Items: items})
}

// DeleteTeamBundle handles DELETE /api/v1/bundles/team/{name}. It detaches
// Admin Humans, deletes Member / Leader Workers, then deletes the Team CR.
// Ordering matters: Team finalizer does not cascade to Workers, so Workers
// must be deleted first to avoid stale teamRef references.
func (h *BundleHandler) DeleteTeamBundle(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		httputil.WriteError(w, http.StatusBadRequest, "team name is required")
		return
	}

	items := make([]BundleResultItem, 0)

	// Phase 1: delete every Worker that still claims membership in this
	// team. Label is maintained by WorkerReconciler to mirror spec.teamRef.
	var workers v1beta1.WorkerList
	if err := h.client.List(r.Context(), &workers,
		client.InNamespace(h.namespace),
		client.MatchingLabels{v1beta1.LabelTeam: name}); err != nil {
		items = append(items, BundleResultItem{
			Kind: "worker", Name: "", Status: "error",
			Message: "list workers: " + err.Error(),
		})
	} else {
		for i := range workers.Items {
			wk := &workers.Items[i]
			if err := h.client.Delete(r.Context(), wk); err != nil {
				if apierrors.IsNotFound(err) {
					items = append(items, BundleResultItem{
						Kind: "worker", Name: wk.Name, Status: "not_found", Warning: true,
					})
					continue
				}
				items = append(items, BundleResultItem{
					Kind: "worker", Name: wk.Name, Status: "error",
					Message: "delete worker: " + err.Error(),
				})
				continue
			}
			items = append(items, BundleResultItem{
				Kind: "worker", Name: wk.Name, Status: "deleted",
			})
		}
	}

	// Phase 2: strip teamAccess entries referencing this team from every
	// Human. Humans without a matching entry are skipped silently to keep
	// the response focused on what actually changed.
	var humans v1beta1.HumanList
	if err := h.client.List(r.Context(), &humans, client.InNamespace(h.namespace)); err != nil {
		items = append(items, BundleResultItem{
			Kind: "human", Name: "", Status: "error",
			Message: "list humans: " + err.Error(),
		})
	} else {
		for i := range humans.Items {
			hu := &humans.Items[i]
			if !humanHasTeamAccess(hu, name) {
				continue
			}
			if err := h.patchHumanTeamAccess(r.Context(), hu, func(entries []v1beta1.TeamAccessEntry) []v1beta1.TeamAccessEntry {
				out := make([]v1beta1.TeamAccessEntry, 0, len(entries))
				for _, e := range entries {
					if e.Team == name {
						continue
					}
					out = append(out, e)
				}
				return out
			}); err != nil {
				items = append(items, BundleResultItem{
					Kind: "human", Name: hu.Name, Status: "error",
					Message: "patch teamAccess: " + err.Error(),
				})
				continue
			}
			items = append(items, BundleResultItem{
				Kind: "human", Name: hu.Name, Status: "patched",
			})
		}
	}

	// Phase 3: delete the Team CR. Done last so that its reconciler does
	// not repeatedly observe soon-to-be-deleted Workers while running.
	team := &v1beta1.Team{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: h.namespace}}
	if err := h.client.Delete(r.Context(), team); err != nil {
		if apierrors.IsNotFound(err) {
			items = append(items, BundleResultItem{
				Kind: "team", Name: name, Status: "not_found", Warning: true,
			})
		} else {
			items = append(items, BundleResultItem{
				Kind: "team", Name: name, Status: "error",
				Message: "delete team: " + err.Error(),
			})
		}
	} else {
		items = append(items, BundleResultItem{
			Kind: "team", Name: name, Status: "deleted",
		})
	}

	httputil.WriteJSON(w, http.StatusMultiStatus, BundleResponse{Items: items})
}

// --- builders ---

func buildTeamFromBundle(req *TeamBundleRequest, ns string) *v1beta1.Team {
	return &v1beta1.Team{
		ObjectMeta: metav1.ObjectMeta{Name: req.Name, Namespace: ns},
		Spec: v1beta1.TeamSpec{
			Description:       req.Description,
			PeerMentions:      req.PeerMentions,
			ChannelPolicy:     req.ChannelPolicy,
			Heartbeat:         req.Heartbeat,
			WorkerIdleTimeout: req.WorkerIdleTimeout,
		},
	}
}

func buildLeaderFromBundle(req *TeamBundleRequest, ns string) *v1beta1.Worker {
	l := req.Leader
	return &v1beta1.Worker{
		ObjectMeta: metav1.ObjectMeta{Name: l.Name, Namespace: ns},
		Spec: v1beta1.WorkerSpec{
			Model:         l.Model,
			Runtime:       l.Runtime,
			Image:         l.Image,
			Role:          v1beta1.WorkerRoleTeamLeader,
			TeamRef:       req.Name,
			Identity:      l.Identity,
			Soul:          l.Soul,
			Agents:        l.Agents,
			Skills:        l.Skills,
			McpServers:    l.McpServers,
			Package:       l.Package,
			Expose:        l.Expose,
			ChannelPolicy: l.ChannelPolicy,
			State:         l.State,
		},
	}
}

func buildMembersFromBundle(req *TeamBundleRequest, ns string) []*v1beta1.Worker {
	out := make([]*v1beta1.Worker, 0, len(req.Workers))
	for _, m := range req.Workers {
		out = append(out, &v1beta1.Worker{
			ObjectMeta: metav1.ObjectMeta{Name: m.Name, Namespace: ns},
			Spec: v1beta1.WorkerSpec{
				Model:         m.Model,
				Runtime:       m.Runtime,
				Image:         m.Image,
				Role:          v1beta1.WorkerRoleTeamWorker,
				TeamRef:       req.Name,
				Identity:      m.Identity,
				Soul:          m.Soul,
				Agents:        m.Agents,
				Skills:        m.Skills,
				McpServers:    m.McpServers,
				Package:       m.Package,
				Expose:        m.Expose,
				ChannelPolicy: m.ChannelPolicy,
				State:         m.State,
			},
		})
	}
	return out
}

// --- admin attachment ---

func (h *BundleHandler) attachAdmin(ctx context.Context, humanName, teamName string) BundleResultItem {
	var hu v1beta1.Human
	if err := h.client.Get(ctx, client.ObjectKey{Name: humanName, Namespace: h.namespace}, &hu); err != nil {
		if apierrors.IsNotFound(err) {
			return BundleResultItem{
				Kind: "human", Name: humanName, Status: "not_found", Warning: true,
				Message: fmt.Sprintf("human %q not found; admin link deferred until Human is applied", humanName),
			}
		}
		return BundleResultItem{
			Kind: "human", Name: humanName, Status: "error",
			Message: "get human: " + err.Error(),
		}
	}

	for _, entry := range hu.Spec.TeamAccess {
		if entry.Team == teamName && entry.Role == v1beta1.TeamAccessRoleAdmin {
			return BundleResultItem{
				Kind: "human", Name: humanName, Status: "skipped",
				Message: "admin relation already present",
			}
		}
	}

	if err := h.patchHumanTeamAccess(ctx, &hu, func(entries []v1beta1.TeamAccessEntry) []v1beta1.TeamAccessEntry {
		return append(entries, v1beta1.TeamAccessEntry{
			Team: teamName,
			Role: v1beta1.TeamAccessRoleAdmin,
		})
	}); err != nil {
		return BundleResultItem{
			Kind: "human", Name: humanName, Status: "error",
			Message: "patch teamAccess: " + err.Error(),
		}
	}
	return BundleResultItem{Kind: "human", Name: humanName, Status: "patched"}
}

// patchHumanTeamAccess applies mutate to a Human's teamAccess slice and
// submits the diff as a merge patch so unrelated fields are untouched.
func (h *BundleHandler) patchHumanTeamAccess(
	ctx context.Context,
	hu *v1beta1.Human,
	mutate func([]v1beta1.TeamAccessEntry) []v1beta1.TeamAccessEntry,
) error {
	patchBase := client.MergeFrom(hu.DeepCopy())
	hu.Spec.TeamAccess = mutate(hu.Spec.TeamAccess)
	return h.client.Patch(ctx, hu, patchBase)
}

func humanHasTeamAccess(hu *v1beta1.Human, teamName string) bool {
	for _, e := range hu.Spec.TeamAccess {
		if e.Team == teamName {
			return true
		}
	}
	return false
}

// --- error mappers ---

func teamItemFromError(name, op string, err error) BundleResultItem {
	status := "error"
	if apierrors.IsAlreadyExists(err) {
		status = "error"
	}
	return BundleResultItem{
		Kind: "team", Name: name, Status: status,
		Message: op + " team: " + err.Error(),
	}
}

func workerItemFromError(name string, err error) BundleResultItem {
	return BundleResultItem{
		Kind: "worker", Name: name, Status: "error",
		Message: "create worker: " + err.Error(),
	}
}
