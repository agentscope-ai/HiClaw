package controller

import (
	v1beta1 "github.com/hiclaw/hiclaw-controller/api/v1beta1"
	"github.com/hiclaw/hiclaw-controller/internal/service"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type managerScope struct {
	manager    *v1beta1.Manager
	provResult *service.ManagerProvisionResult
	patchBase  client.Patch

	// effectiveAllowFrom is the authoritative list of Matrix IDs that
	// should be added to the Manager's groupAllowFrom beyond the default
	// (Manager + Admin). Populated by reconcileManagerAllowFrom by
	// listing standalone + team_leader Workers and superAdmin Humans;
	// consumed by reconcileManagerConfig through the deployer's
	// ChannelPolicy.GroupAllowExtra / DmAllowExtra channel.
	effectiveAllowFrom []string
}

// computeManagerPhase determines the Manager status phase based on reconcile outcome.
// When reconcile succeeds, phase reflects the desired lifecycle state.
// When reconcile fails, phase depends on whether infrastructure was provisioned.
func computeManagerPhase(m *v1beta1.Manager, reconcileErr error) string {
	if reconcileErr != nil {
		if m.Status.MatrixUserID == "" {
			return "Failed"
		}
		if m.Status.Phase == "" {
			return "Pending"
		}
		return m.Status.Phase
	}
	return m.Spec.DesiredState()
}
