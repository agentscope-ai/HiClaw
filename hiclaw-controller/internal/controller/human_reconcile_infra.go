package controller

import (
	"context"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/log"
)

// reconcileHumanInfra brings the Matrix account into the desired state.
//
// First-time provisioning (Status.MatrixUserID == ""):
//   - EnsureHumanUser registers the account and returns a generated
//     password + initial access token. We persist Password
//     (Status.InitialPassword) and the full Matrix user ID
//     (Status.MatrixUserID), and seed scope.userToken with the just-
//     issued token so the subsequent rooms phase can /join without an
//     extra Login round-trip.
//
// Steady-state (Status.MatrixUserID != ""):
//   - **Do nothing.** scope.userToken is intentionally left empty; the
//     rooms phase will call ensureUserToken() *only if* it actually has a
//     new room to /join. This is the laziness that prevents device
//     bloat: the reconciler's periodic 5-minute requeue would otherwise
//     Login on every tick, and `POST /_matrix/client/v3/login` without
//     a device_id creates a fresh device session every time (matching
//     the regression Worker/Manager already fixed via the cached
//     WorkerCredentials.MatrixToken path). A Human has no equivalent
//     credential store, so we avoid the call altogether unless needed.
//
// We deliberately never fall back to EnsureHumanUser after the first
// provisioning: its orphan-recovery branch issues
// "!admin users reset-password" and would silently overwrite a password
// the user may have rotated via Element.
func (r *HumanReconciler) reconcileHumanInfra(ctx context.Context, s *humanScope) error {
	h := s.human

	if h.Status.MatrixUserID != "" {
		return nil
	}

	creds, err := r.Provisioner.EnsureHumanUser(ctx, h.Name)
	if err != nil {
		return fmt.Errorf("matrix registration failed: %w", err)
	}
	h.Status.MatrixUserID = creds.UserID
	h.Status.InitialPassword = creds.Password
	s.userToken = creds.AccessToken

	log.FromContext(ctx).Info("human matrix account provisioned",
		"name", h.Name, "matrixUserID", creds.UserID)
	return nil
}
