package controller

import (
	"context"
	"fmt"
	"time"

	v1 "github.com/hiclaw/hiclaw-controller/api/v1"
	"github.com/hiclaw/hiclaw-controller/internal/executor"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	finalizerName = "hiclaw.io/cleanup"
)

// WorkerReconciler reconciles Worker resources by calling existing bash scripts.
type WorkerReconciler struct {
	client.Client
	Executor *executor.Shell
	Packages *executor.PackageResolver
}

func (r *WorkerReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	logger := log.FromContext(ctx)

	var worker v1.Worker
	if err := r.Get(ctx, req.NamespacedName, &worker); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

	// Handle deletion with finalizer
	if !worker.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(&worker, finalizerName) {
			if err := r.handleDelete(ctx, &worker); err != nil {
				logger.Error(err, "failed to delete worker", "name", worker.Name)
				return reconcile.Result{RequeueAfter: 30 * time.Second}, err
			}
			controllerutil.RemoveFinalizer(&worker, finalizerName)
			if err := r.Update(ctx, &worker); err != nil {
				return reconcile.Result{}, err
			}
		}
		return reconcile.Result{}, nil
	}

	// Add finalizer if not present
	if !controllerutil.ContainsFinalizer(&worker, finalizerName) {
		controllerutil.AddFinalizer(&worker, finalizerName)
		if err := r.Update(ctx, &worker); err != nil {
			return reconcile.Result{}, err
		}
	}

	// Reconcile based on current phase
	switch worker.Status.Phase {
	case "":
		return r.handleCreate(ctx, &worker)
	case "Failed":
		// Retry after backoff
		return r.handleCreate(ctx, &worker)
	default:
		return r.handleUpdate(ctx, &worker)
	}
}

func (r *WorkerReconciler) handleCreate(ctx context.Context, w *v1.Worker) (reconcile.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("creating worker", "name", w.Name)

	w.Status.Phase = "Pending"
	if err := r.Status().Update(ctx, w); err != nil {
		return reconcile.Result{}, err
	}

	// Resolve and deploy package if specified
	if w.Spec.Package != "" {
		extractedDir, err := r.Packages.ResolveAndExtract(ctx, w.Spec.Package, w.Name)
		if err != nil {
			w.Status.Phase = "Failed"
			w.Status.Message = fmt.Sprintf("package resolve/extract failed: %v", err)
			r.Status().Update(ctx, w)
			return reconcile.Result{RequeueAfter: time.Minute}, err
		}
		if extractedDir != "" {
			if err := r.Packages.DeployToMinIO(ctx, extractedDir, w.Name); err != nil {
				w.Status.Phase = "Failed"
				w.Status.Message = fmt.Sprintf("package deploy failed: %v", err)
				r.Status().Update(ctx, w)
				return reconcile.Result{RequeueAfter: time.Minute}, err
			}
			logger.Info("package deployed", "name", w.Name, "dir", extractedDir)
		}
	}

	// Build script arguments
	args := []string{
		"--name", w.Name,
	}
	if w.Spec.Model != "" {
		args = append(args, "--model", w.Spec.Model)
	}
	if w.Spec.Runtime != "" {
		args = append(args, "--runtime", w.Spec.Runtime)
	}
	if w.Spec.Image != "" {
		args = append(args, "--image", w.Spec.Image)
	}
	if len(w.Spec.Skills) > 0 {
		args = append(args, "--skills", joinStrings(w.Spec.Skills))
	}
	if len(w.Spec.McpServers) > 0 {
		args = append(args, "--mcp-servers", joinStrings(w.Spec.McpServers))
	}

	// Check for team annotations (set by TeamReconciler)
	if role := w.Annotations["hiclaw.io/role"]; role != "" {
		args = append(args, "--role", role)
	}
	if team := w.Annotations["hiclaw.io/team"]; team != "" {
		args = append(args, "--team", team)
	}
	if leader := w.Annotations["hiclaw.io/team-leader"]; leader != "" {
		args = append(args, "--team-leader", leader)
	}

	result, err := r.Executor.Run(ctx,
		"/opt/hiclaw/agent/skills/worker-management/scripts/create-worker.sh",
		args...,
	)
	if err != nil {
		w.Status.Phase = "Failed"
		w.Status.Message = fmt.Sprintf("create-worker.sh failed: %v", err)
		r.Status().Update(ctx, w)
		return reconcile.Result{RequeueAfter: time.Minute}, err
	}

	w.Status.Phase = "Running"
	w.Status.MatrixUserID = result.MatrixUserID
	w.Status.RoomID = result.RoomID
	w.Status.Message = ""
	if err := r.Status().Update(ctx, w); err != nil {
		return reconcile.Result{}, err
	}

	logger.Info("worker created", "name", w.Name, "roomID", result.RoomID)
	return reconcile.Result{}, nil
}

func (r *WorkerReconciler) handleUpdate(ctx context.Context, w *v1.Worker) (reconcile.Result, error) {
	// Compare spec hash with last-applied annotation to detect changes
	// For now, no-op if already running
	return reconcile.Result{}, nil
}

func (r *WorkerReconciler) handleDelete(ctx context.Context, w *v1.Worker) error {
	logger := log.FromContext(ctx)
	logger.Info("deleting worker", "name", w.Name)

	// Stop container via lifecycle script
	_, err := r.Executor.RunSimple(ctx,
		"/opt/hiclaw/agent/skills/worker-management/scripts/lifecycle-worker.sh",
		"--action", "stop", "--worker", w.Name,
	)
	if err != nil {
		logger.Error(err, "failed to stop worker container (may already be stopped)", "name", w.Name)
	}

	return nil
}

func joinStrings(ss []string) string {
	result := ""
	for i, s := range ss {
		if i > 0 {
			result += ","
		}
		result += s
	}
	return result
}

// SetupWithManager registers the WorkerReconciler with the controller manager.
func (r *WorkerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1.Worker{}).
		Complete(r)
}
