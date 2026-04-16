package controller

import (
	"context"
	"fmt"

	"github.com/hiclaw/hiclaw-controller/internal/service"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// reconcileInfrastructure ensures Matrix account, Gateway consumer, MinIO user,
// Matrix room, and credentials are provisioned. Idempotent: if already
// provisioned (MatrixUserID set), it refreshes credentials from the persisted store.
func (r *WorkerReconciler) reconcileInfrastructure(ctx context.Context, s *workerScope) (reconcile.Result, error) {
	w := s.worker

	if w.Status.MatrixUserID != "" {
		refreshResult, err := r.Provisioner.RefreshCredentials(ctx, w.Name)
		if err != nil {
			return reconcile.Result{}, fmt.Errorf("refresh credentials: %w", err)
		}
		s.provResult = &service.WorkerProvisionResult{
			MatrixUserID:   w.Status.MatrixUserID,
			MatrixToken:    refreshResult.MatrixToken,
			RoomID:         w.Status.RoomID,
			GatewayKey:     refreshResult.GatewayKey,
			MinIOPassword:  refreshResult.MinIOPassword,
			MatrixPassword: refreshResult.MatrixPassword,
		}
		return reconcile.Result{}, nil
	}

	logger := log.FromContext(ctx)
	logger.Info("provisioning worker infrastructure", "name", w.Name)

	role := w.Annotations["hiclaw.io/role"]
	teamLeaderName := w.Annotations["hiclaw.io/team-leader"]

	provResult, err := r.Provisioner.ProvisionWorker(ctx, service.WorkerProvisionRequest{
		Name:           w.Name,
		Role:           roleForAnnotations(role, teamLeaderName),
		TeamName:       w.Annotations["hiclaw.io/team"],
		TeamLeaderName: teamLeaderName,
		McpServers:     w.Spec.McpServers,
	})
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("provision worker: %w", err)
	}

	w.Status.MatrixUserID = provResult.MatrixUserID
	w.Status.RoomID = provResult.RoomID
	s.provResult = provResult

	if err := r.Provisioner.EnsureServiceAccount(ctx, w.Name); err != nil {
		return reconcile.Result{}, fmt.Errorf("ServiceAccount creation: %w", err)
	}

	return reconcile.Result{}, nil
}
