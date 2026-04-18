package controller

import (
	"context"
	"errors"

	"github.com/hiclaw/hiclaw-controller/internal/backend"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func (r *ManagerReconciler) reconcileManagerDelete(ctx context.Context, s *managerScope) (reconcile.Result, error) {
	logger := log.FromContext(ctx)
	m := s.manager
	logger.Info("deleting manager", "name", m.Name)

	managerName := m.Name

	if err := r.Provisioner.DeactivateMatrixUser(ctx, "manager"); err != nil {
		logger.Error(err, "matrix user deactivation failed (non-fatal)")
	}

	if err := r.Provisioner.DeprovisionManager(ctx, managerName, m.Spec.McpServers); err != nil {
		logger.Error(err, "deprovision failed (non-fatal)")
	}

	if wb := r.managerBackend(ctx); wb != nil {
		containerName := managerContainerName(managerName)
		if err := wb.Delete(ctx, containerName); err != nil && !errors.Is(err, backend.ErrNotFound) {
			logger.Error(err, "failed to delete manager container (may already be removed)")
		}
	}

	if err := r.Deployer.CleanupOSSData(ctx, managerName); err != nil {
		logger.Error(err, "failed to clean up OSS agent data (non-fatal)")
	}
	if err := r.Provisioner.DeleteCredentials(ctx, managerName); err != nil {
		logger.Error(err, "failed to delete credentials (non-fatal)")
	}
	if err := r.Provisioner.DeleteManagerServiceAccount(ctx, managerName); err != nil {
		logger.Error(err, "failed to delete ServiceAccount (non-fatal)")
	}

	controllerutil.RemoveFinalizer(m, finalizerName)
	if err := r.Update(ctx, m); err != nil {
		return reconcile.Result{}, err
	}

	logger.Info("manager deleted", "name", managerName)
	return reconcile.Result{}, nil
}
