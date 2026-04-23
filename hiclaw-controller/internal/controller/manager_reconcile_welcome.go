package controller

import (
	"context"
	"time"

	"github.com/hiclaw/hiclaw-controller/internal/backend"
	"github.com/hiclaw/hiclaw-controller/internal/service"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// welcomeRequeueInterval is how long to wait before re-checking when the
// Manager Matrix user has not yet joined the Admin DM room. Kept short
// because the gap between container start and OpenClaw's first /sync
// auto-join is typically a few seconds; longer than this makes the
// admin's Element Web window sit empty for an uncomfortable time on
// fresh installs. The cost of the 5s loop is one ListRoomMembers HTTP
// call against the local Tuwunel — negligible — and the loop terminates
// the moment the agent's auto-join lands.
const welcomeRequeueInterval = 5 * time.Second

// reconcileManagerWelcome delivers the first-boot onboarding prompt that
// asks the Manager Agent to greet the admin and ask the four identity
// questions (name / language / style / behavior). It is the
// new-architecture replacement for the legacy in-container welcome flow
// that lived in `start-manager-agent.sh` and only ran when
// HICLAW_RUNTIME != "k8s". The legacy path remains untouched for
// docker single-container deploys; in k8s / embedded mode the controller
// owns this responsibility because:
//
//   - it has admin Matrix credentials cached in TuwunelClient already;
//   - it knows when the DM Room was just created (via Status.WelcomeSent);
//   - it does not need to give every Manager container the admin password
//     just to send one prompt at boot.
//
// Sequencing:
//  1. Skip if WelcomeSent already true (idempotency).
//  2. Skip if no RoomID — provisioning hasn't reached Step 4 yet, so
//     reconcileManagerInfrastructure will run again first.
//  3. Skip if the Manager container is not Running/Ready yet — sending
//     before OpenClaw is online means the message lands as a historical
//     event the agent may not act on.
//  4. Ask Provisioner.SendManagerWelcome to verify membership and send.
//     If membership check returns "not joined", requeue after
//     welcomeRequeueInterval.
//  5. On successful send, set WelcomeSent=true so subsequent reconciles
//     are no-ops (and so any Manager container restart never re-prompts).
func (r *ManagerReconciler) reconcileManagerWelcome(ctx context.Context, s *managerScope) (reconcile.Result, error) {
	m := s.manager
	if m.Status.WelcomeSent {
		return reconcile.Result{}, nil
	}
	if m.Status.RoomID == "" {
		return reconcile.Result{}, nil
	}

	logger := log.FromContext(ctx)

	wb := r.managerBackend(ctx)
	if wb != nil {
		st, err := wb.Status(ctx, r.managerContainerName(m.Name))
		if err == nil {
			switch st.Status {
			case backend.StatusRunning, backend.StatusReady:
				// container is live, proceed to send
			default:
				// container not yet usable; rely on the next reconcile
				// (triggered by the Pod-watch mapper for k8s, or the
				// standard reconcileInterval for docker)
				return reconcile.Result{}, nil
			}
		}
	}

	sent, err := r.Provisioner.SendManagerWelcome(ctx, service.ManagerWelcomeRequest{
		RoomID:   m.Status.RoomID,
		Language: r.UserLanguage,
		Timezone: r.UserTimezone,
	})
	if err != nil {
		// Treat all welcome failures as non-fatal so a transient Matrix
		// hiccup never wedges the rest of the manager lifecycle (the
		// agent will still come up; the worst-case observable symptom is
		// "no welcome message", which the admin can prompt around).
		logger.Error(err, "manager welcome send failed (non-fatal, will retry)",
			"manager", m.Name, "roomID", m.Status.RoomID)
		return reconcile.Result{RequeueAfter: welcomeRequeueInterval}, nil
	}
	if !sent {
		logger.V(1).Info("manager not yet joined DM room, requeue for welcome",
			"manager", m.Name, "roomID", m.Status.RoomID)
		return reconcile.Result{RequeueAfter: welcomeRequeueInterval}, nil
	}

	logger.Info("manager onboarding welcome sent", "manager", m.Name, "roomID", m.Status.RoomID)
	m.Status.WelcomeSent = true
	return reconcile.Result{}, nil
}
