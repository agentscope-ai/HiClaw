package controller

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	v1beta1 "github.com/hiclaw/hiclaw-controller/api/v1beta1"
	"github.com/hiclaw/hiclaw-controller/internal/backend"
	"github.com/hiclaw/hiclaw-controller/internal/executor"
	"github.com/hiclaw/hiclaw-controller/internal/service"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/util/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// Team cache field indexer keys. Registered in app.initFieldIndexers and
// consumed by the auth enricher to resolve team membership by worker name
// without enumerating every Team.
const (
	TeamLeaderNameField = "spec.leader.name"
	TeamWorkerNameField = "spec.workerNames"
)

// TeamReconciler reconciles Team resources. It directly owns the lifecycle of
// team members (leader + workers) via the shared member_reconcile helpers; no
// child Worker CRs are created.
type TeamReconciler struct {
	client.Client

	Provisioner service.WorkerProvisioner
	Deployer    service.WorkerDeployer
	Backend     *backend.Registry
	EnvBuilder  service.WorkerEnvBuilderI
	Legacy      *service.LegacyCompat // nil in incluster mode

	AgentFSDir string // for writing inline configs to the local agent FS
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

	return r.reconcileTeamNormal(ctx, &team)
}

// reconcileTeamNormal drives one convergence pass over a Team CR:
//  1. Provision team-level infra (rooms, shared storage)
//  2. Clean up stale members (in Status.ObservedMembers but no longer desired)
//  3. Reconcile each desired member (leader + workers) via the shared phases
//  4. Inject leader coordination context + register with Manager + Legacy
//  5. Summarise backend readiness and patch Team.Status
func (r *TeamReconciler) reconcileTeamNormal(ctx context.Context, t *v1beta1.Team) (reconcile.Result, error) {
	logger := log.FromContext(ctx)

	patchBase := client.MergeFrom(t.DeepCopy())
	if t.Status.Phase == "" {
		t.Status.Phase = "Pending"
		if err := r.Status().Patch(ctx, t, patchBase); err != nil {
			return reconcile.Result{}, err
		}
		patchBase = client.MergeFrom(t.DeepCopy())
	}

	workerNames := make([]string, 0, len(t.Spec.Workers))
	for _, w := range t.Spec.Workers {
		workerNames = append(workerNames, w.Name)
	}

	// --- Step 1: Team-level infrastructure ---
	rooms, err := r.Provisioner.ProvisionTeamRooms(ctx, service.TeamRoomRequest{
		TeamName:       t.Name,
		LeaderName:     t.Spec.Leader.Name,
		WorkerNames:    workerNames,
		AdminSpec:      t.Spec.Admin,
		ExistingRoomID: t.Status.TeamRoomID,
	})
	if err != nil {
		return r.failTeam(ctx, t, patchBase, fmt.Sprintf("provision team rooms: %v", err))
	}
	t.Status.TeamRoomID = rooms.TeamRoomID
	t.Status.LeaderDMRoomID = rooms.LeaderDMRoomID

	if err := r.Deployer.EnsureTeamStorage(ctx, t.Name); err != nil {
		logger.Error(err, "team shared storage init failed (non-fatal)", "name", t.Name)
	}

	// --- Step 2: Write local inline configs (shared FS with agents) ---
	if err := r.writeInlineConfigs(t); err != nil {
		return r.failTeam(ctx, t, patchBase, err.Error())
	}

	// --- Step 3: Stale cleanup ---
	desiredMembers := buildDesiredMembers(t)
	desiredNames := make(map[string]struct{}, len(desiredMembers))
	for _, m := range desiredMembers {
		desiredNames[m.Name] = struct{}{}
	}
	staleNames := make([]string, 0)
	for _, observed := range t.Status.ObservedMembers {
		if _, keep := desiredNames[observed]; !keep {
			staleNames = append(staleNames, observed)
		}
	}
	deps := MemberDeps{
		Provisioner: r.Provisioner,
		Deployer:    r.Deployer,
		Backend:     r.Backend,
		EnvBuilder:  r.EnvBuilder,
	}
	for _, name := range staleNames {
		staleCtx := MemberContext{
			Name:                name,
			Namespace:           t.Namespace,
			Role:                RoleTeamWorker,
			TeamName:            t.Name,
			TeamLeaderName:      t.Spec.Leader.Name,
			CurrentExposedPorts: t.Status.WorkerExposedPorts[name],
		}
		if err := ReconcileMemberDelete(ctx, deps, staleCtx); err != nil {
			logger.Error(err, "failed to remove stale team member (non-fatal)", "name", name)
		}
		delete(t.Status.WorkerExposedPorts, name)
	}

	// --- Step 4: Reconcile each desired member (leader first) ---
	//
	// observedSet tracks which members have successfully completed at least one
	// reconcile pass. A member is considered "observed" iff infra provisioning
	// succeeded at some point (this pass or a prior one). A transient error on
	// an already-observed member does NOT drop it from the set, so subsequent
	// reconciles keep using Refresh. But a member whose first Provision fails
	// stays out of the set, ensuring the next reconcile retries Provision
	// (mirrors the WorkerReconciler semantics driven by Status.MatrixUserID).
	observedSet := make(map[string]struct{}, len(t.Status.ObservedMembers))
	for _, n := range t.Status.ObservedMembers {
		if _, keep := desiredNames[n]; keep {
			observedSet[n] = struct{}{}
		}
	}
	perMemberErrors := make([]string, 0)
	workerExposed := t.Status.WorkerExposedPorts
	if workerExposed == nil {
		workerExposed = make(map[string][]v1beta1.ExposedPortStatus)
	}
	for i := range desiredMembers {
		m := desiredMembers[i]
		if existing, ok := workerExposed[m.Name]; ok {
			m.CurrentExposedPorts = existing
		}
		if err := r.reconcileMember(ctx, deps, m, workerExposed); err != nil {
			logger.Error(err, "team member reconcile failed", "name", m.Name)
			perMemberErrors = append(perMemberErrors, fmt.Sprintf("%s: %v", m.Name, err))
			continue
		}
		observedSet[m.Name] = struct{}{}
	}

	// --- Step 5: Leader-specific hooks (coordination, groupAllowFrom, registry) ---
	var teamAdminID string
	if t.Spec.Admin != nil {
		teamAdminID = t.Spec.Admin.MatrixUserID
	}
	if err := r.Deployer.InjectCoordinationContext(ctx, service.CoordinationDeployRequest{
		LeaderName:        t.Spec.Leader.Name,
		Role:              RoleTeamLeader.String(),
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

	if r.Legacy != nil && r.Legacy.Enabled() {
		leaderMatrixID := r.Legacy.MatrixUserID(t.Spec.Leader.Name)
		if err := r.Legacy.UpdateManagerGroupAllowFrom(leaderMatrixID, true); err != nil {
			logger.Error(err, "failed to update Manager groupAllowFrom for team leader (non-fatal)")
		}
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
	}

	// --- Step 6: Summarise backend readiness and patch status ---
	leaderReady, readyWorkers := r.summarizeBackendReadiness(ctx, desiredMembers)
	observed := make([]string, 0, len(observedSet))
	for n := range observedSet {
		observed = append(observed, n)
	}
	sort.Strings(observed)

	t.Status.ObservedMembers = observed
	t.Status.TotalWorkers = len(t.Spec.Workers)
	t.Status.LeaderReady = leaderReady
	t.Status.ReadyWorkers = readyWorkers
	if len(workerExposed) > 0 {
		t.Status.WorkerExposedPorts = workerExposed
	} else {
		t.Status.WorkerExposedPorts = nil
	}

	switch {
	case len(perMemberErrors) > 0:
		t.Status.Phase = "Degraded"
		t.Status.Message = strings.Join(perMemberErrors, "; ")
	case leaderReady && readyWorkers == t.Status.TotalWorkers:
		t.Status.Phase = "Active"
		t.Status.Message = ""
	default:
		t.Status.Phase = "Pending"
		t.Status.Message = ""
	}

	if err := r.Status().Patch(ctx, t, patchBase); err != nil {
		logger.Error(err, "failed to patch team status (non-fatal)")
	}

	requeue := reconcileInterval
	if len(perMemberErrors) > 0 {
		requeue = reconcileRetryDelay
	}
	logger.Info("team reconciled",
		"name", t.Name,
		"phase", t.Status.Phase,
		"leaderReady", leaderReady,
		"readyWorkers", readyWorkers,
		"observedMembers", observed)
	return reconcile.Result{RequeueAfter: requeue}, nil
}

// reconcileMember runs the shared member phases for one team member and
// accumulates exposed-port state into workerExposed. Leader membership in
// workerExposed is skipped because the leader never exposes gateway ports.
func (r *TeamReconciler) reconcileMember(ctx context.Context, deps MemberDeps, m MemberContext, workerExposed map[string][]v1beta1.ExposedPortStatus) error {
	state := &MemberState{}

	// Pre-populate ExistingMatrixUserID when we've already provisioned the
	// member before, forcing the Refresh path instead of Provision.
	if m.IsUpdate {
		m.ExistingMatrixUserID = r.Provisioner.MatrixUserID(m.Name)
	}

	if _, err := ReconcileMemberInfra(ctx, deps, m, state); err != nil {
		return err
	}
	if err := EnsureMemberServiceAccount(ctx, deps, m); err != nil {
		return err
	}
	if err := ReconcileMemberConfig(ctx, deps, m, state); err != nil {
		return err
	}
	if _, err := ReconcileMemberContainer(ctx, deps, m, state); err != nil {
		return err
	}
	_ = ReconcileMemberExpose(ctx, deps, m, state)

	if m.Role == RoleTeamWorker {
		if len(state.ExposedPorts) > 0 {
			workerExposed[m.Name] = state.ExposedPorts
		} else {
			delete(workerExposed, m.Name)
		}
	}
	return nil
}

// summarizeBackendReadiness queries each member's pod/container status from
// the backend. Used instead of reading Worker CR status because team members
// no longer have Worker CRs.
func (r *TeamReconciler) summarizeBackendReadiness(ctx context.Context, members []MemberContext) (leaderReady bool, readyWorkers int) {
	if r.Backend == nil {
		return false, 0
	}
	wb := r.Backend.DetectWorkerBackend(ctx)
	if wb == nil {
		return false, 0
	}
	for _, m := range members {
		result, err := wb.Status(ctx, m.Name)
		if err != nil {
			continue
		}
		ready := result.Status == backend.StatusRunning || result.Status == backend.StatusReady
		if m.Role == RoleTeamLeader {
			leaderReady = ready
			continue
		}
		if ready {
			readyWorkers++
		}
	}
	return leaderReady, readyWorkers
}

// writeInlineConfigs persists leader + worker inline identity/soul/agents
// strings to the shared agent FS. No-op for members that don't supply any.
func (r *TeamReconciler) writeInlineConfigs(t *v1beta1.Team) error {
	if t.Spec.Leader.Identity != "" || t.Spec.Leader.Soul != "" || t.Spec.Leader.Agents != "" {
		agentDir := fmt.Sprintf("%s/%s", r.AgentFSDir, t.Spec.Leader.Name)
		if err := executor.WriteInlineConfigs(agentDir, "copaw", t.Spec.Leader.Identity, t.Spec.Leader.Soul, t.Spec.Leader.Agents); err != nil {
			return fmt.Errorf("write leader inline configs: %w", err)
		}
	}
	for _, w := range t.Spec.Workers {
		if w.Identity == "" && w.Soul == "" && w.Agents == "" {
			continue
		}
		agentDir := fmt.Sprintf("%s/%s", r.AgentFSDir, w.Name)
		runtime := w.Runtime
		if runtime == "" {
			runtime = "copaw"
		}
		if err := executor.WriteInlineConfigs(agentDir, runtime, w.Identity, w.Soul, w.Agents); err != nil {
			return fmt.Errorf("write worker %s inline configs: %w", w.Name, err)
		}
	}
	return nil
}

func (r *TeamReconciler) handleDelete(ctx context.Context, t *v1beta1.Team) error {
	logger := log.FromContext(ctx)
	logger.Info("deleting team", "name", t.Name)

	deps := MemberDeps{
		Provisioner: r.Provisioner,
		Deployer:    r.Deployer,
		Backend:     r.Backend,
		EnvBuilder:  r.EnvBuilder,
	}

	// Union of ObservedMembers and desired members to guarantee cleanup even
	// when reconcile failed before writing observedMembers.
	names := make(map[string]MemberRole)
	for _, name := range t.Status.ObservedMembers {
		if name == t.Spec.Leader.Name {
			names[name] = RoleTeamLeader
		} else {
			names[name] = RoleTeamWorker
		}
	}
	if t.Spec.Leader.Name != "" {
		names[t.Spec.Leader.Name] = RoleTeamLeader
	}
	for _, w := range t.Spec.Workers {
		names[w.Name] = RoleTeamWorker
	}

	errs := make([]error, 0)
	for name, role := range names {
		mctx := MemberContext{
			Name:                name,
			Namespace:           t.Namespace,
			Role:                role,
			TeamName:            t.Name,
			TeamLeaderName:      t.Spec.Leader.Name,
			CurrentExposedPorts: t.Status.WorkerExposedPorts[name],
		}
		if role == RoleTeamLeader {
			mctx.Spec = leaderWorkerSpec(t)
		} else {
			for _, w := range t.Spec.Workers {
				if w.Name == name {
					mctx.Spec = teamWorkerSpecToWorkerSpec(t, w)
					break
				}
			}
		}
		if err := ReconcileMemberDelete(ctx, deps, mctx); err != nil {
			logger.Error(err, "member cleanup failed (non-fatal)", "name", name)
			errs = append(errs, err)
		}
	}

	if r.Legacy != nil && r.Legacy.Enabled() {
		if t.Spec.Leader.Name != "" {
			leaderMatrixID := r.Legacy.MatrixUserID(t.Spec.Leader.Name)
			if err := r.Legacy.UpdateManagerGroupAllowFrom(leaderMatrixID, false); err != nil {
				logger.Error(err, "failed to revoke Manager groupAllowFrom (non-fatal)")
			}
		}
		if err := r.Legacy.RemoveFromTeamsRegistry(ctx, t.Name); err != nil {
			logger.Error(err, "failed to remove team from registry (non-fatal)")
		}
	}

	if len(errs) > 0 {
		// Errors are non-fatal individually but we return the aggregate so the
		// caller logs one consolidated message; finalizer removal still
		// proceeds at the Reconcile level to avoid stuck CRs.
		return kerrors.NewAggregate(errs)
	}
	return nil
}

func (r *TeamReconciler) failTeam(ctx context.Context, t *v1beta1.Team, patchBase client.Patch, msg string) (reconcile.Result, error) {
	t.Status.Phase = "Failed"
	t.Status.Message = msg
	if err := r.Status().Patch(ctx, t, patchBase); err != nil {
		log.FromContext(ctx).Error(err, "failed to patch team status after failure (non-fatal)")
	}
	return reconcile.Result{RequeueAfter: reconcileRetryDelay}, fmt.Errorf("%s", msg)
}

// --- helpers ---

// buildDesiredMembers translates a Team spec into MemberContexts for leader
// and each worker. Every member is tagged with PodLabel hiclaw.io/team=<name>
// so the Team controller can watch their pod lifecycle via a shared predicate.
func buildDesiredMembers(t *v1beta1.Team) []MemberContext {
	observed := make(map[string]struct{}, len(t.Status.ObservedMembers))
	for _, n := range t.Status.ObservedMembers {
		observed[n] = struct{}{}
	}
	members := make([]MemberContext, 0, 1+len(t.Spec.Workers))

	leaderSpec := leaderWorkerSpec(t)
	_, leaderObserved := observed[t.Spec.Leader.Name]
	members = append(members, MemberContext{
		Name:              t.Spec.Leader.Name,
		Namespace:         t.Namespace,
		Role:              RoleTeamLeader,
		Spec:              leaderSpec,
		Generation:        t.Generation,
		IsUpdate:          leaderObserved,
		TeamName:          t.Name,
		TeamLeaderName:    "",
		TeamAdminMatrixID: teamAdminMatrixID(t),
		PodLabels: map[string]string{
			"hiclaw.io/team": t.Name,
			"hiclaw.io/role": RoleTeamLeader.String(),
		},
	})

	for _, w := range t.Spec.Workers {
		_, workerObserved := observed[w.Name]
		spec := teamWorkerSpecToWorkerSpec(t, w)
		members = append(members, MemberContext{
			Name:              w.Name,
			Namespace:         t.Namespace,
			Role:              RoleTeamWorker,
			Spec:              spec,
			Generation:        t.Generation,
			IsUpdate:          workerObserved,
			TeamName:          t.Name,
			TeamLeaderName:    t.Spec.Leader.Name,
			TeamAdminMatrixID: teamAdminMatrixID(t),
			PodLabels: map[string]string{
				"hiclaw.io/team": t.Name,
				"hiclaw.io/role": RoleTeamWorker.String(),
			},
		})
	}
	return members
}

// leaderWorkerSpec projects a LeaderSpec into WorkerSpec with merged channel
// policy (team leader can @ all members + admin).
func leaderWorkerSpec(t *v1beta1.Team) v1beta1.WorkerSpec {
	policy := mergeChannelPolicy(t.Spec.ChannelPolicy, t.Spec.Leader.ChannelPolicy)
	workerNames := make([]string, 0, len(t.Spec.Workers))
	for _, w := range t.Spec.Workers {
		workerNames = append(workerNames, w.Name)
	}
	policy = appendGroupAllowExtra(policy, workerNames...)
	if t.Spec.Admin != nil && t.Spec.Admin.Name != "" {
		policy = appendGroupAllowExtra(policy, t.Spec.Admin.Name)
		policy = appendDmAllowExtra(policy, t.Spec.Admin.Name)
	}
	return v1beta1.WorkerSpec{
		Model:         t.Spec.Leader.Model,
		Runtime:       "copaw",
		Identity:      t.Spec.Leader.Identity,
		Soul:          t.Spec.Leader.Soul,
		Agents:        t.Spec.Leader.Agents,
		Package:       t.Spec.Leader.Package,
		ChannelPolicy: policy,
		State:         t.Spec.Leader.State,
	}
}

// teamWorkerSpecToWorkerSpec projects a TeamWorkerSpec into WorkerSpec with
// the policy merge rules:
//   - leader is always on the worker's groupAllow
//   - team admin (if any) is on the worker's groupAllow
//   - if Team.Spec.PeerMentions is true (default), all peers are groupAllow too
func teamWorkerSpecToWorkerSpec(t *v1beta1.Team, w v1beta1.TeamWorkerSpec) v1beta1.WorkerSpec {
	policy := mergeChannelPolicy(t.Spec.ChannelPolicy, w.ChannelPolicy)
	policy = appendGroupAllowExtra(policy, t.Spec.Leader.Name)
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
	return v1beta1.WorkerSpec{
		Model:         w.Model,
		Runtime:       "copaw",
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
	}
}

func teamAdminMatrixID(t *v1beta1.Team) string {
	if t.Spec.Admin == nil {
		return ""
	}
	return t.Spec.Admin.MatrixUserID
}

func (r *TeamReconciler) SetupWithManager(mgr ctrl.Manager) error {
	bldr := ctrl.NewControllerManagedBy(mgr).For(&v1beta1.Team{})

	if r.Backend != nil {
		if wb := r.Backend.DetectWorkerBackend(context.Background()); wb != nil && wb.Name() == "k8s" {
			bldr = bldr.Watches(
				&corev1.Pod{},
				handler.EnqueueRequestsFromMapFunc(func(_ context.Context, obj client.Object) []reconcile.Request {
					teamName := obj.GetLabels()["hiclaw.io/team"]
					if teamName == "" {
						return nil
					}
					return []reconcile.Request{
						{NamespacedName: client.ObjectKey{
							Name:      teamName,
							Namespace: obj.GetNamespace(),
						}},
					}
				}),
				builder.WithPredicates(podLifecyclePredicates("hiclaw.io/team")),
			)
		}
	}

	return bldr.Complete(r)
}

// --- Policy helpers (preserved from prior implementation) ---

func leaderHeartbeatEvery(team *v1beta1.Team) string {
	if team.Spec.Leader.Heartbeat == nil {
		return ""
	}
	return team.Spec.Leader.Heartbeat.Every
}

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
