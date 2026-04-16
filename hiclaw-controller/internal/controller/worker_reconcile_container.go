package controller

import (
	"context"
	"errors"
	"fmt"

	authpkg "github.com/hiclaw/hiclaw-controller/internal/auth"
	"github.com/hiclaw/hiclaw-controller/internal/backend"
	"github.com/hiclaw/hiclaw-controller/internal/service"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// reconcileContainer ensures the worker container/pod matches the desired
// lifecycle state (Running/Sleeping/Stopped). Idempotent: checks actual
// backend state before acting.
func (r *WorkerReconciler) reconcileContainer(ctx context.Context, s *workerScope) (reconcile.Result, error) {
	if s.provResult == nil {
		return reconcile.Result{}, nil
	}

	w := s.worker
	desired := w.Spec.DesiredState()

	switch desired {
	case "Stopped":
		return r.ensureContainerAbsent(ctx, s, true)
	case "Sleeping":
		return r.ensureContainerAbsent(ctx, s, false)
	default:
		return r.ensureContainerPresent(ctx, s)
	}
}

// ensureContainerPresent ensures a worker container is running. If the
// container does not exist or was deleted, it is (re)created. If the spec
// has changed (Generation != ObservedGeneration) while the container is
// running, the old container is deleted and a new one created so the worker
// picks up the latest configuration on startup.
func (r *WorkerReconciler) ensureContainerPresent(ctx context.Context, s *workerScope) (reconcile.Result, error) {
	w := s.worker
	if r.Backend == nil {
		return reconcile.Result{}, nil
	}
	wb := r.Backend.DetectWorkerBackend(ctx)
	if wb == nil {
		log.FromContext(ctx).Info("no worker backend available, worker needs manual start")
		return reconcile.Result{}, nil
	}

	logger := log.FromContext(ctx)
	result, err := wb.Status(ctx, w.Name)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("query container status: %w", err)
	}

	specChanged := w.Generation != w.Status.ObservedGeneration

	switch result.Status {
	case backend.StatusRunning, backend.StatusStarting, backend.StatusReady:
		if !specChanged {
			return reconcile.Result{}, nil
		}
		logger.Info("spec changed, recreating container",
			"generation", w.Generation,
			"observedGeneration", w.Status.ObservedGeneration)
		if err := wb.Delete(ctx, w.Name); err != nil && !errors.Is(err, backend.ErrNotFound) {
			return reconcile.Result{}, fmt.Errorf("delete container for recreate: %w", err)
		}
		return r.createWorkerContainer(ctx, s, wb)

	case backend.StatusStopped:
		if wb.Name() == "docker" && !specChanged {
			if err := wb.Start(ctx, w.Name); err != nil {
				return reconcile.Result{}, fmt.Errorf("start container: %w", err)
			}
			return reconcile.Result{}, nil
		}
		if err := wb.Delete(ctx, w.Name); err != nil && !errors.Is(err, backend.ErrNotFound) {
			return reconcile.Result{}, fmt.Errorf("delete stale container: %w", err)
		}
		return r.createWorkerContainer(ctx, s, wb)

	case backend.StatusNotFound:
		return r.createWorkerContainer(ctx, s, wb)

	default:
		logger.Info("container in unexpected state, recreating", "status", result.Status)
		if err := wb.Delete(ctx, w.Name); err != nil && !errors.Is(err, backend.ErrNotFound) {
			return reconcile.Result{}, fmt.Errorf("delete container in unknown state: %w", err)
		}
		return r.createWorkerContainer(ctx, s, wb)
	}
}

// ensureContainerAbsent ensures a worker container is not running.
// If remove is true (Stopped), the container is deleted entirely.
// If remove is false (Sleeping), the container is stopped but kept (Docker)
// or deleted (K8s, which has no stop-without-delete).
func (r *WorkerReconciler) ensureContainerAbsent(ctx context.Context, s *workerScope, remove bool) (reconcile.Result, error) {
	w := s.worker
	if r.Backend == nil {
		return reconcile.Result{}, nil
	}
	wb := r.Backend.DetectWorkerBackend(ctx)
	if wb == nil {
		return reconcile.Result{}, nil
	}

	if remove {
		if err := wb.Delete(ctx, w.Name); err != nil && !errors.Is(err, backend.ErrNotFound) {
			return reconcile.Result{}, fmt.Errorf("delete container: %w", err)
		}
	} else {
		if err := wb.Stop(ctx, w.Name); err != nil && !errors.Is(err, backend.ErrNotFound) {
			return reconcile.Result{}, fmt.Errorf("stop container: %w", err)
		}
	}

	return reconcile.Result{}, nil
}

// createWorkerContainer builds and issues a backend Create request.
// ErrConflict (container already exists) is treated as success for idempotency.
func (r *WorkerReconciler) createWorkerContainer(ctx context.Context, s *workerScope, wb backend.WorkerBackend) (reconcile.Result, error) {
	w := s.worker
	logger := log.FromContext(ctx)

	prov := s.provResult
	if prov.MatrixToken == "" {
		refreshResult, err := r.Provisioner.RefreshCredentials(ctx, w.Name)
		if err != nil {
			return reconcile.Result{}, fmt.Errorf("refresh credentials for container: %w", err)
		}
		prov = &service.WorkerProvisionResult{
			MatrixUserID:   w.Status.MatrixUserID,
			MatrixToken:    refreshResult.MatrixToken,
			RoomID:         w.Status.RoomID,
			GatewayKey:     refreshResult.GatewayKey,
			MinIOPassword:  refreshResult.MinIOPassword,
			MatrixPassword: refreshResult.MatrixPassword,
		}
	}

	workerEnv := r.EnvBuilder.Build(w.Name, prov)
	saName := authpkg.SAName(authpkg.RoleWorker, w.Name)
	createReq := backend.CreateRequest{
		Name:               w.Name,
		Image:              w.Spec.Image,
		Runtime:            w.Spec.Runtime,
		Env:                workerEnv,
		ServiceAccountName: saName,
	}
	if wb.Name() != "k8s" {
		token, err := r.Provisioner.RequestSAToken(ctx, w.Name)
		if err != nil {
			logger.Error(err, "SA token request failed (non-fatal, worker auth will fail)")
		}
		createReq.AuthToken = token
	}

	if _, err := wb.Create(ctx, createReq); err != nil {
		if errors.Is(err, backend.ErrConflict) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, fmt.Errorf("create container: %w", err)
	}

	return reconcile.Result{}, nil
}
