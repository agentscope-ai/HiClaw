package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	v1beta1 "github.com/hiclaw/hiclaw-controller/api/v1beta1"
	"github.com/hiclaw/hiclaw-controller/internal/executor"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// TeamReconciler reconciles Team resources.
type TeamReconciler struct {
	client.Client
	Executor *executor.Shell
	Packages *executor.PackageResolver
}

func (r *TeamReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	logger := log.FromContext(ctx)

	var team v1beta1.Team
	if err := r.Get(ctx, req.NamespacedName, &team); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	// Handle deletion
	if !team.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(&team, finalizerName) {
			if err := r.handleDelete(ctx, &team); err != nil {
				logger.Error(err, "failed to delete team", "name", team.Name)
				return reconcile.Result{RequeueAfter: 30 * time.Second}, err
			}
			controllerutil.RemoveFinalizer(&team, finalizerName)
			if err := r.Update(ctx, &team); err != nil {
				return reconcile.Result{}, err
			}
		}
		return reconcile.Result{}, nil
	}

	// Add finalizer
	if !controllerutil.ContainsFinalizer(&team, finalizerName) {
		controllerutil.AddFinalizer(&team, finalizerName)
		if err := r.Update(ctx, &team); err != nil {
			return reconcile.Result{}, err
		}
	}

	switch team.Status.Phase {
	case "":
		return r.handleCreate(ctx, &team)
	case "Failed":
		return r.handleCreate(ctx, &team)
	default:
		return r.handleUpdate(ctx, &team)
	}
}

func (r *TeamReconciler) handleCreate(ctx context.Context, t *v1beta1.Team) (reconcile.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("creating team", "name", t.Name)

	t.Status.Phase = "Pending"
	t.Status.TotalWorkers = len(t.Spec.Workers)
	if err := r.Status().Update(ctx, t); err != nil {
		return reconcile.Result{}, err
	}

	// Build worker names CSV
	workerNames := make([]string, 0, len(t.Spec.Workers))
	for _, w := range t.Spec.Workers {
		workerNames = append(workerNames, w.Name)
	}

	// Write leader inline configs
	if t.Spec.Leader.Identity != "" || t.Spec.Leader.Soul != "" || t.Spec.Leader.Agents != "" {
		agentDir := fmt.Sprintf("/root/hiclaw-fs/agents/%s", t.Spec.Leader.Name)
		if err := executor.WriteInlineConfigs(agentDir, "", t.Spec.Leader.Identity, t.Spec.Leader.Soul, t.Spec.Leader.Agents); err != nil {
			t.Status.Phase = "Failed"
			t.Status.Message = fmt.Sprintf("write leader inline configs failed: %v", err)
			r.Status().Update(ctx, t)
			return reconcile.Result{RequeueAfter: time.Minute}, err
		}
		logger.Info("leader inline configs written", "leader", t.Spec.Leader.Name)
	}

	// Write each worker's inline configs
	for _, w := range t.Spec.Workers {
		if w.Identity != "" || w.Soul != "" || w.Agents != "" {
			agentDir := fmt.Sprintf("/root/hiclaw-fs/agents/%s", w.Name)
			if err := executor.WriteInlineConfigs(agentDir, w.Runtime, w.Identity, w.Soul, w.Agents); err != nil {
				t.Status.Phase = "Failed"
				t.Status.Message = fmt.Sprintf("write worker %s inline configs failed: %v", w.Name, err)
				r.Status().Update(ctx, t)
				return reconcile.Result{RequeueAfter: time.Minute}, err
			}
			logger.Info("worker inline configs written", "worker", w.Name)
		}
	}

	args := []string{
		"--name", t.Name,
		"--leader", t.Spec.Leader.Name,
		"--workers", strings.Join(workerNames, ","),
	}
	if t.Spec.Leader.Model != "" {
		args = append(args, "--leader-model", t.Spec.Leader.Model)
	}

	// Build worker models CSV if any specified
	workerModels := make([]string, 0, len(t.Spec.Workers))
	for _, w := range t.Spec.Workers {
		workerModels = append(workerModels, w.Model)
	}
	hasModels := false
	for _, m := range workerModels {
		if m != "" {
			hasModels = true
			break
		}
	}
	if hasModels {
		args = append(args, "--worker-models", strings.Join(workerModels, ","))
	}

	// Peer mentions toggle (default true, only pass when explicitly false)
	if t.Spec.PeerMentions != nil && !*t.Spec.PeerMentions {
		args = append(args, "--peer-mentions", "false")
	}

	// Team-level comm policy
	if t.Spec.ChannelPolicy != nil {
		if policyJSON, err := json.Marshal(t.Spec.ChannelPolicy); err == nil {
			args = append(args, "--team-channel-policy", string(policyJSON))
		}
	}

	// Leader comm policy
	if t.Spec.Leader.ChannelPolicy != nil {
		if policyJSON, err := json.Marshal(t.Spec.Leader.ChannelPolicy); err == nil {
			args = append(args, "--leader-channel-policy", string(policyJSON))
		}
	}

	// Per-worker comm policies (: separated, aligned with worker names CSV)
	workerPolicies := make([]string, len(t.Spec.Workers))
	hasPolicies := false
	for i, w := range t.Spec.Workers {
		if w.ChannelPolicy != nil {
			if p, err := json.Marshal(w.ChannelPolicy); err == nil {
				workerPolicies[i] = string(p)
				hasPolicies = true
			}
		}
	}
	if hasPolicies {
		args = append(args, "--worker-channel-policies", strings.Join(workerPolicies, "|"))
	}

	// Per-worker skills (: separated)
	workerSkills := make([]string, len(t.Spec.Workers))
	hasSkills := false
	for i, w := range t.Spec.Workers {
		if len(w.Skills) > 0 {
			workerSkills[i] = joinStrings(w.Skills)
			hasSkills = true
		}
	}
	if hasSkills {
		args = append(args, "--worker-skills", strings.Join(workerSkills, ":"))
	}

	// Per-worker MCP servers (: separated)
	workerMcpServers := make([]string, len(t.Spec.Workers))
	hasMcpServers := false
	for i, w := range t.Spec.Workers {
		if len(w.McpServers) > 0 {
			workerMcpServers[i] = joinStrings(w.McpServers)
			hasMcpServers = true
		}
	}
	if hasMcpServers {
		args = append(args, "--worker-mcp-servers", strings.Join(workerMcpServers, ":"))
	}

	// Team admin
	if t.Spec.Admin != nil {
		if t.Spec.Admin.Name != "" {
			args = append(args, "--team-admin", t.Spec.Admin.Name)
		}
		if t.Spec.Admin.MatrixUserID != "" {
			args = append(args, "--team-admin-matrix-id", t.Spec.Admin.MatrixUserID)
		}
	}

	result, err := r.Executor.Run(ctx,
		"/opt/hiclaw/agent/skills/team-management/scripts/create-team.sh",
		args...,
	)
	if err != nil {
		t.Status.Phase = "Failed"
		t.Status.Message = fmt.Sprintf("create-team.sh failed: %v", err)
		r.Status().Update(ctx, t)
		return reconcile.Result{RequeueAfter: time.Minute}, err
	}

	t.Status.Phase = "Active"
	t.Status.LeaderReady = true
	t.Status.ReadyWorkers = len(t.Spec.Workers)
	t.Status.Message = ""
	if result.JSON != nil {
		if rid, ok := result.JSON["team_room_id"].(string); ok {
			t.Status.TeamRoomID = rid
		}
	}
	if err := r.Status().Update(ctx, t); err != nil {
		return reconcile.Result{}, err
	}

	logger.Info("team created", "name", t.Name, "teamRoomID", t.Status.TeamRoomID)
	return reconcile.Result{}, nil
}

func (r *TeamReconciler) handleUpdate(ctx context.Context, t *v1beta1.Team) (reconcile.Result, error) {
	// TODO: detect worker list changes and add/remove workers
	return reconcile.Result{}, nil
}

func (r *TeamReconciler) handleDelete(ctx context.Context, t *v1beta1.Team) error {
	logger := log.FromContext(ctx)
	logger.Info("deleting team", "name", t.Name)

	// Delete all team workers first, then leader
	for _, w := range t.Spec.Workers {
		r.Executor.RunSimple(ctx,
			"/opt/hiclaw/agent/skills/worker-management/scripts/lifecycle-worker.sh",
			"--action", "delete", "--worker", w.Name,
		)
	}
	r.Executor.RunSimple(ctx,
		"/opt/hiclaw/agent/skills/worker-management/scripts/lifecycle-worker.sh",
		"--action", "delete", "--worker", t.Spec.Leader.Name,
	)

	// Remove from teams-registry
	r.Executor.RunSimple(ctx,
		"/opt/hiclaw/agent/skills/team-management/scripts/manage-teams-registry.sh",
		"--action", "remove", "--team-name", t.Name,
	)

	return nil
}

// SetupWithManager registers the TeamReconciler with the controller manager.
func (r *TeamReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1beta1.Team{}).
		Complete(r)
}