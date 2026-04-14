package controller

import (
	"context"
	"fmt"
	"time"

	v1beta1 "github.com/hiclaw/hiclaw-controller/api/v1beta1"
	"github.com/hiclaw/hiclaw-controller/internal/executor"
	"github.com/hiclaw/hiclaw-controller/internal/service"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// TeamReconciler reconciles Team resources using Service-layer orchestration.
type TeamReconciler struct {
	client.Client

	Provisioner *service.Provisioner
	Deployer    *service.Deployer
	Legacy      *service.LegacyCompat // nil in incluster mode

	AgentFSDir string // for writing inline configs to local FS
}

func (r *TeamReconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	logger := log.FromContext(ctx)

	var team v1beta1.Team
	if err := r.Get(ctx, req.NamespacedName, &team); err != nil {
		return reconcile.Result{}, client.IgnoreNotFound(err)
	}

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

	if !controllerutil.ContainsFinalizer(&team, finalizerName) {
		controllerutil.AddFinalizer(&team, finalizerName)
		if err := r.Update(ctx, &team); err != nil {
			return reconcile.Result{}, err
		}
	}

	switch team.Status.Phase {
	case "", "Failed":
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

	workerNames := make([]string, 0, len(t.Spec.Workers))
	for _, w := range t.Spec.Workers {
		workerNames = append(workerNames, w.Name)
	}

	// --- Step 1: Create Team Room + Leader DM Room ---
	rooms, err := r.Provisioner.ProvisionTeamRooms(ctx, service.TeamRoomRequest{
		TeamName:       t.Name,
		LeaderName:     t.Spec.Leader.Name,
		WorkerNames:    workerNames,
		AdminSpec:      t.Spec.Admin,
		ExistingRoomID: t.Status.TeamRoomID,
	})
	if err != nil {
		return r.failTeam(ctx, t, err.Error())
	}
	t.Status.TeamRoomID = rooms.TeamRoomID
	t.Status.LeaderDMRoomID = rooms.LeaderDMRoomID

	// --- Step 2: Write inline configs for leader + workers ---
	if t.Spec.Leader.Identity != "" || t.Spec.Leader.Soul != "" || t.Spec.Leader.Agents != "" {
		agentDir := fmt.Sprintf("%s/%s", r.AgentFSDir, t.Spec.Leader.Name)
		if err := executor.WriteInlineConfigs(agentDir, "", t.Spec.Leader.Identity, t.Spec.Leader.Soul, t.Spec.Leader.Agents); err != nil {
			return r.failTeam(ctx, t, fmt.Sprintf("write leader inline configs failed: %v", err))
		}
	}
	for _, w := range t.Spec.Workers {
		if w.Identity != "" || w.Soul != "" || w.Agents != "" {
			agentDir := fmt.Sprintf("%s/%s", r.AgentFSDir, w.Name)
			if err := executor.WriteInlineConfigs(agentDir, w.Runtime, w.Identity, w.Soul, w.Agents); err != nil {
				return r.failTeam(ctx, t, fmt.Sprintf("write worker %s inline configs failed: %v", w.Name, err))
			}
		}
	}

	// --- Step 3: Create Worker CRs (leader + workers) ---
	leaderCR := r.buildLeaderCR(t)
	if err := r.createOrUpdateWorkerCR(ctx, leaderCR); err != nil {
		return r.failTeam(ctx, t, fmt.Sprintf("create leader Worker CR failed: %v", err))
	}
	logger.Info("leader Worker CR created", "name", t.Spec.Leader.Name)

	for _, w := range t.Spec.Workers {
		workerCR := r.buildWorkerCR(t, w, "worker", t.Spec.Leader.Name, t.Name)
		if err := r.createOrUpdateWorkerCR(ctx, workerCR); err != nil {
			logger.Error(err, "failed to create team worker CR (non-fatal)", "worker", w.Name)
		} else {
			logger.Info("team worker CR created", "name", w.Name)
		}
	}

	// --- Step 4: Inject coordination context for leader ---
	var teamAdminID string
	if t.Spec.Admin != nil {
		teamAdminID = t.Spec.Admin.MatrixUserID
	}
	if err := r.Deployer.InjectCoordinationContext(ctx, service.CoordinationDeployRequest{
		LeaderName:        t.Spec.Leader.Name,
		Role:              "team_leader",
		TeamName:          t.Name,
		TeamRoomID:        rooms.TeamRoomID,
		LeaderDMRoomID:    rooms.LeaderDMRoomID,
		HeartbeatEvery:    leaderHeartbeatEvery(t),
		WorkerIdleTimeout: t.Spec.Leader.WorkerIdleTimeout,
		TeamWorkers:       workerNames,
		TeamAdminID:       teamAdminID,
	}); err != nil {
		logger.Error(err, "leader coordination context injection failed (non-fatal)")
	}

	// --- Step 5: Expose ports for team workers ---
	workerExposed := make(map[string][]v1beta1.ExposedPortStatus)
	for _, w := range t.Spec.Workers {
		if len(w.Expose) > 0 {
			exposed, exposeErr := r.Provisioner.ReconcileExpose(ctx, w.Name, w.Expose, nil)
			if exposeErr != nil {
				logger.Error(exposeErr, "failed to expose ports for team worker (non-fatal)", "worker", w.Name)
			}
			if len(exposed) > 0 {
				workerExposed[w.Name] = exposed
			}
		}
	}

	// --- Step 6: Team shared storage ---
	if err := r.Deployer.EnsureTeamStorage(ctx, t.Name); err != nil {
		logger.Error(err, "team shared storage init failed (non-fatal)")
	}

	// --- Step 7: Legacy teams-registry + Manager groupAllowFrom ---
	if r.Legacy != nil && r.Legacy.Enabled() {
		if err := r.Legacy.UpdateTeamsRegistry(service.TeamRegistryEntry{
			Name:           t.Name,
			Leader:         t.Spec.Leader.Name,
			Workers:        workerNames,
			TeamRoomID:     rooms.TeamRoomID,
			LeaderDMRoomID: rooms.LeaderDMRoomID,
			Admin:          teamAdminRegistryEntry(t.Spec.Admin),
		}); err != nil {
			logger.Error(err, "teams-registry update failed (non-fatal)")
		}
		// Add team leader to Manager's groupAllowFrom so Manager can receive leader messages
		leaderMatrixID := r.Legacy.MatrixUserID(t.Spec.Leader.Name)
		if err := r.Legacy.UpdateManagerGroupAllowFrom(leaderMatrixID, true); err != nil {
			logger.Error(err, "failed to update Manager groupAllowFrom for team leader (non-fatal)")
		}
	}

	_ = r.Get(ctx, client.ObjectKeyFromObject(t), t)
	t.Status.Phase = "Active"
	t.Status.LeaderReady = true
	t.Status.ReadyWorkers = len(t.Spec.Workers)
	t.Status.TeamRoomID = rooms.TeamRoomID
	t.Status.LeaderDMRoomID = rooms.LeaderDMRoomID
	t.Status.Message = ""
	if len(workerExposed) > 0 {
		t.Status.WorkerExposedPorts = workerExposed
	}
	if err := r.Status().Update(ctx, t); err != nil {
		logger.Error(err, "failed to update team status (non-fatal)")
	}

	logger.Info("team created", "name", t.Name, "teamRoomID", rooms.TeamRoomID)
	return reconcile.Result{}, nil
}

func (r *TeamReconciler) handleUpdate(ctx context.Context, t *v1beta1.Team) (reconcile.Result, error) {
	logger := log.FromContext(ctx)
	logger.Info("updating team", "name", t.Name)

	leaderCR := r.buildLeaderCR(t)
	if err := r.createOrUpdateWorkerCR(ctx, leaderCR); err != nil {
		return r.failTeam(ctx, t, fmt.Sprintf("update leader Worker CR failed: %v", err))
	}

	desiredWorkers := make(map[string]v1beta1.TeamWorkerSpec, len(t.Spec.Workers))
	workerNames := make([]string, 0, len(t.Spec.Workers))
	for _, workerSpec := range t.Spec.Workers {
		desiredWorkers[workerSpec.Name] = workerSpec
		workerNames = append(workerNames, workerSpec.Name)

		workerCR := r.buildWorkerCR(t, workerSpec, "worker", t.Spec.Leader.Name, t.Name)
		if err := r.createOrUpdateWorkerCR(ctx, workerCR); err != nil {
			return r.failTeam(ctx, t, fmt.Sprintf("update team worker %s failed: %v", workerSpec.Name, err))
		}
	}

	var existingWorkers v1beta1.WorkerList
	if err := r.List(ctx, &existingWorkers, client.InNamespace(t.Namespace), client.MatchingLabels{"hiclaw.io/team": t.Name}); err != nil {
		return reconcile.Result{}, err
	}
	for i := range existingWorkers.Items {
		existing := &existingWorkers.Items[i]
		if existing.Name == t.Spec.Leader.Name {
			continue
		}
		if _, keep := desiredWorkers[existing.Name]; keep {
			continue
		}
		if err := r.deleteWorkerAndExpose(ctx, t, existing); err != nil {
			logger.Error(err, "failed to remove stale team worker", "worker", existing.Name)
		}
	}

	var teamAdminID string
	if t.Spec.Admin != nil {
		teamAdminID = t.Spec.Admin.MatrixUserID
	}
	if err := r.Deployer.InjectCoordinationContext(ctx, service.CoordinationDeployRequest{
		LeaderName:        t.Spec.Leader.Name,
		Role:              "team_leader",
		TeamName:          t.Name,
		TeamRoomID:        t.Status.TeamRoomID,
		LeaderDMRoomID:    t.Status.LeaderDMRoomID,
		HeartbeatEvery:    leaderHeartbeatEvery(t),
		WorkerIdleTimeout: t.Spec.Leader.WorkerIdleTimeout,
		TeamWorkers:       workerNames,
		TeamAdminID:       teamAdminID,
	}); err != nil {
		logger.Error(err, "leader coordination context refresh failed (non-fatal)")
	}

	_ = r.Get(ctx, client.ObjectKeyFromObject(t), t)
	t.Status.TotalWorkers = len(t.Spec.Workers)
	t.Status.ReadyWorkers, t.Status.LeaderReady = summarizeTeamWorkerReadiness(existingWorkers.Items, t.Spec.Leader.Name)
	if t.Status.Phase == "" {
		t.Status.Phase = "Active"
	}
	if t.Status.WorkerExposedPorts != nil {
		for workerName := range t.Status.WorkerExposedPorts {
			if _, keep := desiredWorkers[workerName]; !keep {
				delete(t.Status.WorkerExposedPorts, workerName)
			}
		}
	}
	t.Status.Message = ""
	if err := r.Status().Update(ctx, t); err != nil {
		logger.Error(err, "failed to update team status after update (non-fatal)")
	}

	return reconcile.Result{}, nil
}

func (r *TeamReconciler) handleDelete(ctx context.Context, t *v1beta1.Team) error {
	logger := log.FromContext(ctx)
	logger.Info("deleting team", "name", t.Name)

	// Clean up exposed ports for team workers
	for _, w := range t.Spec.Workers {
		var currentExposed []v1beta1.ExposedPortStatus
		if t.Status.WorkerExposedPorts != nil {
			currentExposed = t.Status.WorkerExposedPorts[w.Name]
		}
		if len(currentExposed) == 0 && len(w.Expose) > 0 {
			for _, ep := range w.Expose {
				currentExposed = append(currentExposed, v1beta1.ExposedPortStatus{
					Port:   ep.Port,
					Domain: service.ContainerDNSName(w.Name),
				})
			}
		}
		if len(currentExposed) > 0 {
			if _, err := r.Provisioner.ReconcileExpose(ctx, w.Name, nil, currentExposed); err != nil {
				logger.Error(err, "failed to clean up exposed ports for team worker (non-fatal)", "worker", w.Name)
			}
		}
	}

	// Delete Worker CRs (workers first, then leader)
	ns := t.Namespace
	for _, w := range t.Spec.Workers {
		workerCR := &v1beta1.Worker{}
		workerCR.Name = w.Name
		workerCR.Namespace = ns
		if err := r.Delete(ctx, workerCR); err != nil {
			logger.Error(err, "failed to delete team worker CR (may not exist)", "worker", w.Name)
		}
	}
	leaderCR := &v1beta1.Worker{}
	leaderCR.Name = t.Spec.Leader.Name
	leaderCR.Namespace = ns
	if err := r.Delete(ctx, leaderCR); err != nil {
		logger.Error(err, "failed to delete team leader CR (may not exist)", "leader", t.Spec.Leader.Name)
	}

	// Legacy: remove team from teams-registry
	if r.Legacy != nil {
		if err := r.Legacy.RemoveFromTeamsRegistry(ctx, t.Name); err != nil {
			logger.Error(err, "failed to remove team from registry (non-fatal)")
		}
	}

	logger.Info("team deleted", "name", t.Name)
	return nil
}

func (r *TeamReconciler) buildLeaderCR(t *v1beta1.Team) *v1beta1.Worker {
	policy := mergeChannelPolicy(t.Spec.ChannelPolicy, t.Spec.Leader.ChannelPolicy)

	allWorkerNames := make([]string, 0, len(t.Spec.Workers))
	for _, w := range t.Spec.Workers {
		allWorkerNames = append(allWorkerNames, w.Name)
	}
	policy = appendGroupAllowExtra(policy, allWorkerNames...)

	if t.Spec.Admin != nil && t.Spec.Admin.Name != "" {
		policy = appendGroupAllowExtra(policy, t.Spec.Admin.Name)
		policy = appendDmAllowExtra(policy, t.Spec.Admin.Name)
	}

	annotations := map[string]string{
		"hiclaw.io/role": "team_leader",
		"hiclaw.io/team": t.Name,
	}
	if t.Spec.Admin != nil && t.Spec.Admin.MatrixUserID != "" {
		annotations["hiclaw.io/team-admin-id"] = t.Spec.Admin.MatrixUserID
	}

	return &v1beta1.Worker{
		ObjectMeta: metav1.ObjectMeta{
			Name:        t.Spec.Leader.Name,
			Namespace:   t.Namespace,
			Annotations: annotations,
			Labels: map[string]string{
				"hiclaw.io/team": t.Name,
				"hiclaw.io/role": "team_leader",
			},
		},
		Spec: v1beta1.WorkerSpec{
			Model:         t.Spec.Leader.Model,
			Runtime:       "copaw", // Team mode requires copaw runtime for leader too
			Identity:      t.Spec.Leader.Identity,
			Soul:          t.Spec.Leader.Soul,
			Agents:        t.Spec.Leader.Agents,
			Package:       t.Spec.Leader.Package,
			ChannelPolicy: policy,
			State:         t.Spec.Leader.State,
		},
	}
}

func (r *TeamReconciler) buildWorkerCR(t *v1beta1.Team, w v1beta1.TeamWorkerSpec, role, leaderName, teamName string) *v1beta1.Worker {
	annotations := map[string]string{
		"hiclaw.io/role": role,
		"hiclaw.io/team": teamName,
	}
	if leaderName != "" {
		annotations["hiclaw.io/team-leader"] = leaderName
	}
	if t.Spec.Admin != nil && t.Spec.Admin.MatrixUserID != "" {
		annotations["hiclaw.io/team-admin-id"] = t.Spec.Admin.MatrixUserID
	}

	policy := mergeChannelPolicy(t.Spec.ChannelPolicy, w.ChannelPolicy)
	policy = appendGroupAllowExtra(policy, leaderName)

	if t.Spec.Admin != nil && t.Spec.Admin.Name != "" {
		policy = appendGroupAllowExtra(policy, t.Spec.Admin.Name)
	}

	peerMentions := t.Spec.PeerMentions == nil || *t.Spec.PeerMentions
	if peerMentions {
		for _, peer := range t.Spec.Workers {
			if peer.Name != w.Name {
				policy = appendGroupAllowExtra(policy, peer.Name)
			}
		}
	}

	return &v1beta1.Worker{
		ObjectMeta: metav1.ObjectMeta{
			Name:        w.Name,
			Namespace:   t.Namespace,
			Annotations: annotations,
			Labels: map[string]string{
				"hiclaw.io/team": teamName,
				"hiclaw.io/role": role,
			},
		},
		Spec: v1beta1.WorkerSpec{
			Model:         w.Model,
			Runtime:       "copaw", // Team mode requires copaw runtime
			Image:         w.Image,
			Identity:      w.Identity,
			Soul:          w.Soul,
			Agents:        w.Agents,
			Skills:        w.Skills,
			McpServers:    w.McpServers,
			Package:       w.Package,
			Expose:        w.Expose,
			ChannelPolicy: policy,
			State:         w.State,
		},
	}
}

func (r *TeamReconciler) createOrUpdateWorkerCR(ctx context.Context, desired *v1beta1.Worker) error {
	existing := &v1beta1.Worker{}
	err := r.Get(ctx, client.ObjectKeyFromObject(desired), existing)
	if err != nil {
		if client.IgnoreNotFound(err) != nil {
			return err
		}
		return r.Create(ctx, desired)
	}
	existing.Spec = desired.Spec
	if existing.Annotations == nil {
		existing.Annotations = make(map[string]string)
	}
	for k, v := range desired.Annotations {
		existing.Annotations[k] = v
	}
	return r.Update(ctx, existing)
}

func (r *TeamReconciler) failTeam(ctx context.Context, t *v1beta1.Team, msg string) (reconcile.Result, error) {
	_ = r.Get(ctx, client.ObjectKeyFromObject(t), t)
	t.Status.Phase = "Failed"
	t.Status.Message = msg
	r.Status().Update(ctx, t)
	return reconcile.Result{RequeueAfter: time.Minute}, fmt.Errorf("%s", msg)
}

func (r *TeamReconciler) deleteWorkerAndExpose(ctx context.Context, team *v1beta1.Team, worker *v1beta1.Worker) error {
	var currentExposed []v1beta1.ExposedPortStatus
	if team.Status.WorkerExposedPorts != nil {
		currentExposed = team.Status.WorkerExposedPorts[worker.Name]
	}
	if len(currentExposed) == 0 && len(worker.Spec.Expose) > 0 {
		for _, ep := range worker.Spec.Expose {
			currentExposed = append(currentExposed, v1beta1.ExposedPortStatus{
				Port:   ep.Port,
				Domain: service.ContainerDNSName(worker.Name),
			})
		}
	}
	if len(currentExposed) > 0 {
		if _, err := r.Provisioner.ReconcileExpose(ctx, worker.Name, nil, currentExposed); err != nil {
			return err
		}
	}
	return r.Delete(ctx, worker)
}

func leaderHeartbeatEvery(team *v1beta1.Team) string {
	if team.Spec.Leader.Heartbeat == nil {
		return ""
	}
	return team.Spec.Leader.Heartbeat.Every
}

func summarizeTeamWorkerReadiness(workers []v1beta1.Worker, leaderName string) (int, bool) {
	readyWorkers := 0
	leaderReady := false
	for _, worker := range workers {
		isReady := worker.Status.Phase == "Running" || worker.Status.Phase == "Ready"
		if worker.Name == leaderName {
			leaderReady = isReady
			continue
		}
		if isReady {
			readyWorkers++
		}
	}
	return readyWorkers, leaderReady
}

func (r *TeamReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&v1beta1.Team{}).
		Complete(r)
}

// mergeChannelPolicy produces a merged ChannelPolicySpec from a team-wide base
// and an individual override. Both may be nil.
func mergeChannelPolicy(teamPolicy, individualPolicy *v1beta1.ChannelPolicySpec) *v1beta1.ChannelPolicySpec {
	if teamPolicy == nil && individualPolicy == nil {
		return nil
	}
	merged := &v1beta1.ChannelPolicySpec{}
	if teamPolicy != nil {
		merged.GroupAllowExtra = append(merged.GroupAllowExtra, teamPolicy.GroupAllowExtra...)
		merged.GroupDenyExtra = append(merged.GroupDenyExtra, teamPolicy.GroupDenyExtra...)
		merged.DmAllowExtra = append(merged.DmAllowExtra, teamPolicy.DmAllowExtra...)
		merged.DmDenyExtra = append(merged.DmDenyExtra, teamPolicy.DmDenyExtra...)
	}
	if individualPolicy != nil {
		merged.GroupAllowExtra = append(merged.GroupAllowExtra, individualPolicy.GroupAllowExtra...)
		merged.GroupDenyExtra = append(merged.GroupDenyExtra, individualPolicy.GroupDenyExtra...)
		merged.DmAllowExtra = append(merged.DmAllowExtra, individualPolicy.DmAllowExtra...)
		merged.DmDenyExtra = append(merged.DmDenyExtra, individualPolicy.DmDenyExtra...)
	}
	return merged
}

// appendGroupAllowExtra adds names to GroupAllowExtra, creating the policy if nil.
func appendGroupAllowExtra(policy *v1beta1.ChannelPolicySpec, names ...string) *v1beta1.ChannelPolicySpec {
	if len(names) == 0 {
		return policy
	}
	if policy == nil {
		policy = &v1beta1.ChannelPolicySpec{}
	}
	existing := make(map[string]bool, len(policy.GroupAllowExtra))
	for _, v := range policy.GroupAllowExtra {
		existing[v] = true
	}
	for _, n := range names {
		if n != "" && !existing[n] {
			policy.GroupAllowExtra = append(policy.GroupAllowExtra, n)
			existing[n] = true
		}
	}
	return policy
}

// appendDmAllowExtra adds names to DmAllowExtra, creating the policy if nil.
func appendDmAllowExtra(policy *v1beta1.ChannelPolicySpec, names ...string) *v1beta1.ChannelPolicySpec {
	if len(names) == 0 {
		return policy
	}
	if policy == nil {
		policy = &v1beta1.ChannelPolicySpec{}
	}
	existing := make(map[string]bool, len(policy.DmAllowExtra))
	for _, v := range policy.DmAllowExtra {
		existing[v] = true
	}
	for _, n := range names {
		if n != "" && !existing[n] {
			policy.DmAllowExtra = append(policy.DmAllowExtra, n)
			existing[n] = true
		}
	}
	return policy
}

func teamAdminRegistryEntry(admin *v1beta1.TeamAdminSpec) *service.TeamAdminEntry {
	if admin == nil {
		return nil
	}
	return &service.TeamAdminEntry{
		Name:         admin.Name,
		MatrixUserID: admin.MatrixUserID,
	}
}
