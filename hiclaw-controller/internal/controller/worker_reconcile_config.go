package controller

import (
	"context"
	"fmt"

	"github.com/hiclaw/hiclaw-controller/internal/service"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// reconcileConfig ensures all configuration (package, inline configs, openclaw.json,
// SOUL.md, mcporter config, AGENTS.md, builtin skills) is deployed to OSS.
// Idempotent: safe to re-run; OSS writes overwrite existing files.
func (r *WorkerReconciler) reconcileConfig(ctx context.Context, s *workerScope) (reconcile.Result, error) {
	if s.provResult == nil {
		return reconcile.Result{}, nil
	}

	w := s.worker
	logger := log.FromContext(ctx)
	workerName := w.Name
	role := w.Annotations["hiclaw.io/role"]
	teamName := w.Annotations["hiclaw.io/team"]
	teamLeaderName := w.Annotations["hiclaw.io/team-leader"]
	teamAdminMatrixID := w.Annotations["hiclaw.io/team-admin-id"]
	consumerName := "worker-" + workerName

	isUpdate := w.Status.Phase != "" && w.Status.Phase != "Pending" && w.Status.Phase != "Failed"

	if err := r.Deployer.DeployPackage(ctx, workerName, w.Spec.Package, isUpdate); err != nil {
		return reconcile.Result{}, fmt.Errorf("deploy package: %w", err)
	}
	if err := r.Deployer.WriteInlineConfigs(workerName, w.Spec); err != nil {
		return reconcile.Result{}, fmt.Errorf("write inline configs: %w", err)
	}

	var authorizedMCPs []string
	if isUpdate && len(w.Spec.McpServers) > 0 {
		var err error
		authorizedMCPs, err = r.Provisioner.ReconcileMCPAuth(ctx, consumerName, w.Spec.McpServers)
		if err != nil {
			logger.Error(err, "MCP reauthorization failed (non-fatal)")
		}
	} else {
		authorizedMCPs = s.provResult.AuthorizedMCPs
	}

	if err := r.Deployer.DeployWorkerConfig(ctx, service.WorkerDeployRequest{
		Name:              workerName,
		Spec:              w.Spec,
		Role:              roleForAnnotations(role, teamLeaderName),
		TeamName:          teamName,
		TeamLeaderName:    teamLeaderName,
		MatrixToken:       s.provResult.MatrixToken,
		GatewayKey:        s.provResult.GatewayKey,
		MatrixPassword:    s.provResult.MatrixPassword,
		AuthorizedMCPs:    authorizedMCPs,
		TeamAdminMatrixID: teamAdminMatrixID,
		IsUpdate:          isUpdate,
	}); err != nil {
		return reconcile.Result{}, fmt.Errorf("deploy worker config: %w", err)
	}

	if err := r.Deployer.PushOnDemandSkills(ctx, workerName, w.Spec.Skills); err != nil {
		logger.Error(err, "skill push failed (non-fatal)")
	}

	return reconcile.Result{}, nil
}
