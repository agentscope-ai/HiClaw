package controller

import (
	"context"
	"fmt"

	"github.com/hiclaw/hiclaw-controller/internal/service"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// reconcileManagerConfig ensures all configuration (package, manager config,
// on-demand skills) is deployed to OSS. Idempotent: safe to re-run; OSS writes
// overwrite existing files.
func (r *ManagerReconciler) reconcileManagerConfig(ctx context.Context, s *managerScope) (reconcile.Result, error) {
	if s.provResult == nil {
		return reconcile.Result{}, nil
	}

	m := s.manager
	logger := log.FromContext(ctx)
	isUpdate := m.Status.Phase != "" && m.Status.Phase != "Pending" && m.Status.Phase != "Failed"

	if err := r.Deployer.DeployPackage(ctx, m.Name, m.Spec.Package, isUpdate); err != nil {
		return reconcile.Result{}, fmt.Errorf("deploy package: %w", err)
	}

	var authorizedMCPs []string
	if isUpdate && len(m.Spec.McpServers) > 0 {
		var err error
		authorizedMCPs, err = r.Provisioner.ReconcileMCPAuth(ctx, "manager", m.Spec.McpServers)
		if err != nil {
			logger.Error(err, "MCP reauthorization failed (non-fatal)")
		}
	} else {
		authorizedMCPs = s.provResult.AuthorizedMCPs
	}

	if err := r.Deployer.DeployManagerConfig(ctx, service.ManagerDeployRequest{
		Name:           m.Name,
		Spec:           m.Spec,
		MatrixToken:    s.provResult.MatrixToken,
		GatewayKey:     s.provResult.GatewayKey,
		MatrixPassword: s.provResult.MatrixPassword,
		AuthorizedMCPs: authorizedMCPs,
		IsUpdate:       isUpdate,
	}); err != nil {
		return reconcile.Result{}, fmt.Errorf("deploy manager config: %w", err)
	}

	if err := r.Deployer.PushOnDemandSkills(ctx, m.Name, m.Spec.Skills); err != nil {
		logger.Error(err, "skill push failed (non-fatal)")
	}

	return reconcile.Result{}, nil
}
