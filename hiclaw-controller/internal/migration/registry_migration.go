package migration

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	v1beta1 "github.com/hiclaw/hiclaw-controller/api/v1beta1"
	"github.com/hiclaw/hiclaw-controller/internal/oss"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
)

const (
	migrationMarker = "agents/manager/.migration-v1beta1-done"
)

// Migrator converts v1.0.9 registry JSON files into CR resources on controller startup.
type Migrator struct {
	OSS          oss.StorageClient
	RestCfg      *rest.Config
	Namespace    string
	DefaultModel string
	ManagerName  string // default "manager"
	AgentFSDir   string // local filesystem root for agent workspaces (e.g. /root/hiclaw-fs/agents)
}

func (m *Migrator) managerName() string {
	if m.ManagerName != "" {
		return m.ManagerName
	}
	return "manager"
}

func (m *Migrator) Run(ctx context.Context) error {
	logger := ctrl.Log.WithName("migration")

	// Idempotency: skip if already done
	if err := m.OSS.Stat(ctx, migrationMarker); err == nil {
		logger.Info("registry migration already completed, skipping")
		return nil
	}

	dynClient, err := dynamic.NewForConfig(m.RestCfg)
	if err != nil {
		return fmt.Errorf("create dynamic client: %w", err)
	}

	workersReg, err := m.loadWorkersRegistry(ctx)
	if err != nil {
		return fmt.Errorf("load workers registry: %w", err)
	}
	teamsReg, err := m.loadTeamsRegistry(ctx)
	if err != nil {
		return fmt.Errorf("load teams registry: %w", err)
	}
	humansReg, err := m.loadHumansRegistry(ctx)
	if err != nil {
		return fmt.Errorf("load humans registry: %w", err)
	}

	if len(workersReg) == 0 && len(teamsReg) == 0 && len(humansReg) == 0 {
		logger.Info("no registry data found, marking migration complete")
		return m.writeMarker(ctx)
	}

	// Build team lookup: workerName -> teamName
	teamByWorker := make(map[string]string)
	for teamName, entry := range teamsReg {
		teamByWorker[entry.Leader] = teamName
		for _, w := range entry.Workers {
			teamByWorker[w] = teamName
		}
	}

	workerRes := dynClient.Resource(workerGVR).Namespace(m.Namespace)

	// Step 1: Create standalone Worker CRs (workers not belonging to any team)
	for name, entry := range workersReg {
		if _, inTeam := teamByWorker[name]; inTeam {
			continue
		}
		if err := m.createStandaloneWorkerCR(ctx, workerRes, name, entry); err != nil {
			logger.Error(err, "failed to migrate standalone worker (non-fatal)", "worker", name)
		} else {
			logger.Info("migrated standalone worker", "name", name)
		}
	}

	// Step 2: Create Worker CRs for team members with channelPolicy matching
	// Team reconciler's buildLeaderCR/buildWorkerCR output exactly.
	// This must happen BEFORE Team CRs so that Team reconciler's handleUpdate
	// finds existing Worker CRs with matching spec (no generation bump).
	for teamName, teamEntry := range teamsReg {
		// Leader
		if leaderEntry, ok := workersReg[teamEntry.Leader]; ok {
			if err := m.createTeamLeaderWorkerCR(ctx, workerRes, teamName, teamEntry, leaderEntry); err != nil {
				logger.Error(err, "failed to migrate team leader (non-fatal)", "worker", teamEntry.Leader)
			} else {
				logger.Info("migrated team leader", "name", teamEntry.Leader)
			}
		} else {
			logger.Info("team leader not found in workers-registry", "team", teamName, "leader", teamEntry.Leader)
		}
		// Workers
		for _, wName := range teamEntry.Workers {
			if wEntry, ok := workersReg[wName]; ok {
				if err := m.createTeamMemberWorkerCR(ctx, workerRes, wName, teamName, teamEntry, wEntry); err != nil {
					logger.Error(err, "failed to migrate team worker (non-fatal)", "worker", wName)
				} else {
					logger.Info("migrated team worker", "name", wName)
				}
			} else {
				logger.Info("team worker not found in workers-registry", "team", teamName, "worker", wName)
			}
		}
	}

	// Step 3: Create Team CRs (which reference already-created Worker CRs)
	for teamName, teamEntry := range teamsReg {
		if err := m.createTeamCR(ctx, dynClient, teamName, teamEntry, workersReg); err != nil {
			logger.Error(err, "failed to migrate team (non-fatal)", "team", teamName)
		} else {
			logger.Info("migrated team", "name", teamName)
		}
	}

	// Step 4: Create Human CRs
	for name, entry := range humansReg {
		if err := m.createHumanCR(ctx, dynClient, name, entry); err != nil {
			logger.Error(err, "failed to migrate human (non-fatal)", "human", name)
		} else {
			logger.Info("migrated human", "name", name)
		}
	}

	return m.writeMarker(ctx)
}

// --- Registry types ---

type workersRegistry struct {
	Version   int                       `json:"version"`
	UpdatedAt string                    `json:"updated_at"`
	Workers   map[string]workerRegEntry `json:"workers"`
}

type workerRegEntry struct {
	MatrixUserID    string   `json:"matrix_user_id"`
	RoomID          string   `json:"room_id"`
	Runtime         string   `json:"runtime"`
	Deployment      string   `json:"deployment"`
	Skills          []string `json:"skills"`
	Role            string   `json:"role"`
	TeamID          *string  `json:"team_id"`
	Image           *string  `json:"image"`
	CreatedAt       string   `json:"created_at,omitempty"`
	SkillsUpdatedAt string   `json:"skills_updated_at"`
}

type teamsRegistry struct {
	Version   int                     `json:"version"`
	UpdatedAt string                  `json:"updated_at"`
	Teams     map[string]teamRegEntry `json:"teams"`
}

type teamRegEntry struct {
	Leader         string        `json:"leader"`
	Workers        []string      `json:"workers"`
	TeamRoomID     string        `json:"team_room_id"`
	LeaderDMRoomID string        `json:"leader_dm_room_id,omitempty"`
	Admin          *teamAdminReg `json:"admin,omitempty"`
	CreatedAt      string        `json:"created_at,omitempty"`
}

type teamAdminReg struct {
	Name         string `json:"name"`
	MatrixUserID string `json:"matrix_user_id"`
}

type humansRegistry struct {
	Version   int                      `json:"version"`
	UpdatedAt string                   `json:"updated_at"`
	Humans    map[string]humanRegEntry `json:"humans"`
}

type humanRegEntry struct {
	MatrixUserID    string   `json:"matrix_user_id"`
	DisplayName     string   `json:"display_name"`
	PermissionLevel int      `json:"permission_level"`
	AccessibleTeams []string `json:"accessible_teams,omitempty"`
	CreatedAt       string   `json:"created_at,omitempty"`
}

// --- Registry loading (local FS first, fallback to OSS) ---

func (m *Migrator) loadWorkersRegistry(ctx context.Context) (map[string]workerRegEntry, error) {
	data, err := m.readRegistryFile("workers-registry.json")
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var reg workersRegistry
	if err := json.Unmarshal(data, &reg); err != nil {
		return nil, fmt.Errorf("parse workers-registry.json: %w", err)
	}
	return reg.Workers, nil
}

func (m *Migrator) loadTeamsRegistry(ctx context.Context) (map[string]teamRegEntry, error) {
	data, err := m.readRegistryFile("teams-registry.json")
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var reg teamsRegistry
	if err := json.Unmarshal(data, &reg); err != nil {
		return nil, fmt.Errorf("parse teams-registry.json: %w", err)
	}
	return reg.Teams, nil
}

func (m *Migrator) loadHumansRegistry(ctx context.Context) (map[string]humanRegEntry, error) {
	data, err := m.readRegistryFile("humans-registry.json")
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var reg humansRegistry
	if err := json.Unmarshal(data, &reg); err != nil {
		return nil, fmt.Errorf("parse humans-registry.json: %w", err)
	}
	return reg.Humans, nil
}

func (m *Migrator) readRegistryFile(filename string) ([]byte, error) {
	if m.AgentFSDir != "" {
		localPath := filepath.Join(m.AgentFSDir, m.managerName(), filename)
		data, err := os.ReadFile(localPath)
		if err == nil {
			return data, nil
		}
		if !os.IsNotExist(err) {
			return nil, err
		}
	}
	key := fmt.Sprintf("agents/%s/%s", m.managerName(), filename)
	return m.OSS.GetObject(context.Background(), key)
}

// --- Workspace data extraction ---

func (m *Migrator) extractModel(ctx context.Context, workerName string) string {
	data := m.readAgentFile(ctx, workerName, "openclaw.json")
	if data == nil {
		return m.DefaultModel
	}
	var cfg map[string]interface{}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return m.DefaultModel
	}
	models, _ := cfg["models"].(map[string]interface{})
	if models == nil {
		return m.DefaultModel
	}
	defaultModel, _ := models["default"].(string)
	if defaultModel == "" {
		return m.DefaultModel
	}
	return strings.TrimPrefix(defaultModel, "hiclaw-gateway/")
}

func (m *Migrator) extractMCPServers(ctx context.Context, workerName string) []string {
	data := m.readAgentFile(ctx, workerName, "mcporter-servers.json")
	if data == nil {
		return nil
	}
	var servers map[string]interface{}
	if err := json.Unmarshal(data, &servers); err != nil {
		return nil
	}
	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, name)
	}
	return names
}

func (m *Migrator) readAgentFile(ctx context.Context, workerName, filename string) []byte {
	if m.AgentFSDir != "" {
		localPath := filepath.Join(m.AgentFSDir, workerName, filename)
		data, err := os.ReadFile(localPath)
		if err == nil {
			return data
		}
	}
	key := fmt.Sprintf("agents/%s/%s", workerName, filename)
	data, err := m.OSS.GetObject(ctx, key)
	if err != nil {
		return nil
	}
	return data
}

// --- CR creation ---

var (
	workerGVR = schema.GroupVersionResource{Group: v1beta1.GroupName, Version: v1beta1.Version, Resource: "workers"}
	teamGVR   = schema.GroupVersionResource{Group: v1beta1.GroupName, Version: v1beta1.Version, Resource: "teams"}
	humanGVR  = schema.GroupVersionResource{Group: v1beta1.GroupName, Version: v1beta1.Version, Resource: "humans"}
)

// createStandaloneWorkerCR creates a Worker CR for a worker not belonging to any team.
func (m *Migrator) createStandaloneWorkerCR(ctx context.Context, res dynamic.ResourceInterface, name string, entry workerRegEntry) error {
	if _, err := res.Get(ctx, name, metav1.GetOptions{}); err == nil {
		return nil
	}

	model := m.extractModel(ctx, name)
	mcpServers := m.extractMCPServers(ctx, name)

	role := entry.Role
	if role == "" {
		role = "standalone"
	}

	spec := map[string]interface{}{
		"model":   model,
		"runtime": entry.Runtime,
	}
	if entry.Image != nil && *entry.Image != "" {
		spec["image"] = *entry.Image
	}
	if len(entry.Skills) > 0 {
		spec["skills"] = toInterfaceSlice(entry.Skills)
	}
	if len(mcpServers) > 0 {
		spec["mcpServers"] = toInterfaceSlice(mcpServers)
	}

	obj := buildWorkerUnstructured(m.Namespace, name, role, "", "", "", spec)
	created, err := res.Create(ctx, obj, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("create Worker CR %s: %w", name, err)
	}
	return m.setWorkerStatus(ctx, res, created, entry.MatrixUserID, entry.RoomID)
}

// createTeamLeaderWorkerCR creates a Worker CR for a team leader with channelPolicy
// matching TeamReconciler.buildLeaderCR exactly.
func (m *Migrator) createTeamLeaderWorkerCR(ctx context.Context, res dynamic.ResourceInterface, teamName string, team teamRegEntry, entry workerRegEntry) error {
	name := team.Leader
	if _, err := res.Get(ctx, name, metav1.GetOptions{}); err == nil {
		return nil
	}

	model := m.extractModel(ctx, name)

	// Build channelPolicy matching team_controller.go buildLeaderCR:
	// 1. appendGroupAllowExtra(policy, allWorkerNames...)
	// 2. if admin: appendGroupAllowExtra(admin.Name), appendDmAllowExtra(admin.Name)
	var groupAllow []string
	groupAllow = append(groupAllow, team.Workers...)
	var dmAllow []string
	if team.Admin != nil && team.Admin.Name != "" {
		groupAllow = appendUnique(groupAllow, team.Admin.Name)
		dmAllow = appendUnique(dmAllow, team.Admin.Name)
	}

	spec := map[string]interface{}{
		"model":   model,
		"runtime": "copaw",
	}
	if len(groupAllow) > 0 || len(dmAllow) > 0 {
		cp := map[string]interface{}{}
		if len(groupAllow) > 0 {
			cp["groupAllowExtra"] = toInterfaceSlice(groupAllow)
		}
		if len(dmAllow) > 0 {
			cp["dmAllowExtra"] = toInterfaceSlice(dmAllow)
		}
		spec["channelPolicy"] = cp
	}

	adminMatrixID := ""
	if team.Admin != nil {
		adminMatrixID = team.Admin.MatrixUserID
	}

	obj := buildWorkerUnstructured(m.Namespace, name, "team_leader", teamName, "", adminMatrixID, spec)
	created, err := res.Create(ctx, obj, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("create Worker CR %s: %w", name, err)
	}
	return m.setWorkerStatus(ctx, res, created, entry.MatrixUserID, entry.RoomID)
}

// createTeamMemberWorkerCR creates a Worker CR for a team worker with channelPolicy
// matching TeamReconciler.buildWorkerCR exactly.
func (m *Migrator) createTeamMemberWorkerCR(ctx context.Context, res dynamic.ResourceInterface, name, teamName string, team teamRegEntry, entry workerRegEntry) error {
	if _, err := res.Get(ctx, name, metav1.GetOptions{}); err == nil {
		return nil
	}

	model := m.extractModel(ctx, name)
	mcpServers := m.extractMCPServers(ctx, name)

	// Build channelPolicy matching team_controller.go buildWorkerCR:
	// 1. appendGroupAllowExtra(policy, leaderName)
	// 2. if admin: appendGroupAllowExtra(admin.Name)
	// 3. peerMentions (default true): appendGroupAllowExtra for each peer
	var groupAllow []string
	groupAllow = appendUnique(groupAllow, team.Leader)
	if team.Admin != nil && team.Admin.Name != "" {
		groupAllow = appendUnique(groupAllow, team.Admin.Name)
	}
	// peerMentions defaults to true (PeerMentions == nil || *PeerMentions)
	for _, peer := range team.Workers {
		if peer != name {
			groupAllow = appendUnique(groupAllow, peer)
		}
	}

	spec := map[string]interface{}{
		"model":   model,
		"runtime": "copaw",
	}
	if entry.Image != nil && *entry.Image != "" {
		spec["image"] = *entry.Image
	}
	if len(entry.Skills) > 0 {
		spec["skills"] = toInterfaceSlice(entry.Skills)
	}
	if len(mcpServers) > 0 {
		spec["mcpServers"] = toInterfaceSlice(mcpServers)
	}
	if len(groupAllow) > 0 {
		spec["channelPolicy"] = map[string]interface{}{
			"groupAllowExtra": toInterfaceSlice(groupAllow),
		}
	}

	adminMatrixID := ""
	if team.Admin != nil {
		adminMatrixID = team.Admin.MatrixUserID
	}

	obj := buildWorkerUnstructured(m.Namespace, name, "worker", teamName, team.Leader, adminMatrixID, spec)
	created, err := res.Create(ctx, obj, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("create Worker CR %s: %w", name, err)
	}
	return m.setWorkerStatus(ctx, res, created, entry.MatrixUserID, entry.RoomID)
}

// buildWorkerUnstructured constructs a Worker CR with proper metadata.
func buildWorkerUnstructured(namespace, name, role, teamName, teamLeader, teamAdminMatrixID string, spec map[string]interface{}) *unstructured.Unstructured {
	annotations := map[string]interface{}{
		"hiclaw.io/role": role,
	}
	labels := map[string]interface{}{
		"hiclaw.io/role": role,
	}
	if teamName != "" {
		annotations["hiclaw.io/team"] = teamName
		labels["hiclaw.io/team"] = teamName
	}
	if teamLeader != "" {
		annotations["hiclaw.io/team-leader"] = teamLeader
	}
	if teamAdminMatrixID != "" {
		annotations["hiclaw.io/team-admin-id"] = teamAdminMatrixID
	}

	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": v1beta1.GroupName + "/" + v1beta1.Version,
			"kind":       "Worker",
			"metadata": map[string]interface{}{
				"name":        name,
				"namespace":   namespace,
				"annotations": annotations,
				"labels":      labels,
				"finalizers":  []interface{}{"hiclaw.io/cleanup"},
			},
			"spec": spec,
		},
	}
}

func (m *Migrator) setWorkerStatus(ctx context.Context, res dynamic.ResourceInterface, obj *unstructured.Unstructured, matrixUserID, roomID string) error {
	fresh, err := res.Get(ctx, obj.GetName(), metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("re-read Worker %s for status: %w", obj.GetName(), err)
	}
	status := map[string]interface{}{
		"phase":              "Running",
		"matrixUserID":       matrixUserID,
		"roomID":             roomID,
		"observedGeneration": fresh.GetGeneration(),
	}
	if err := unstructured.SetNestedField(fresh.Object, status, "status"); err != nil {
		return fmt.Errorf("set status on Worker %s: %w", obj.GetName(), err)
	}
	_, err = res.UpdateStatus(ctx, fresh, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("update Worker %s status: %w", obj.GetName(), err)
	}
	return nil
}

// --- Team CR ---

func (m *Migrator) createTeamCR(ctx context.Context, dynClient dynamic.Interface, teamName string, entry teamRegEntry, workersReg map[string]workerRegEntry) error {
	res := dynClient.Resource(teamGVR).Namespace(m.Namespace)

	if _, err := res.Get(ctx, teamName, metav1.GetOptions{}); err == nil {
		return nil
	}

	logger := ctrl.Log.WithName("migration")

	leaderModel := m.extractModel(ctx, entry.Leader)
	leader := map[string]interface{}{
		"name":  entry.Leader,
		"model": leaderModel,
	}

	workers := make([]interface{}, 0, len(entry.Workers))
	for _, wName := range entry.Workers {
		wModel := m.extractModel(ctx, wName)
		wSpec := map[string]interface{}{
			"name":  wName,
			"model": wModel,
		}
		if wEntry, ok := workersReg[wName]; ok {
			if len(wEntry.Skills) > 0 {
				wSpec["skills"] = toInterfaceSlice(wEntry.Skills)
			}
			mcpServers := m.extractMCPServers(ctx, wName)
			if len(mcpServers) > 0 {
				wSpec["mcpServers"] = toInterfaceSlice(mcpServers)
			}
			if wEntry.Image != nil && *wEntry.Image != "" {
				wSpec["image"] = *wEntry.Image
			}
		} else {
			logger.Info("team worker not found in workers-registry", "team", teamName, "worker", wName)
		}
		workers = append(workers, wSpec)
	}

	spec := map[string]interface{}{
		"leader":  leader,
		"workers": workers,
	}
	if entry.Admin != nil {
		admin := map[string]interface{}{
			"name": entry.Admin.Name,
		}
		if entry.Admin.MatrixUserID != "" {
			admin["matrixUserId"] = entry.Admin.MatrixUserID
		}
		spec["admin"] = admin
	}

	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": v1beta1.GroupName + "/" + v1beta1.Version,
			"kind":       "Team",
			"metadata": map[string]interface{}{
				"name":       teamName,
				"namespace":  m.Namespace,
				"finalizers": []interface{}{"hiclaw.io/cleanup"},
			},
			"spec": spec,
		},
	}

	created, err := res.Create(ctx, obj, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("create Team CR %s: %w", teamName, err)
	}
	return m.setTeamStatus(ctx, res, created, entry)
}

func (m *Migrator) setTeamStatus(ctx context.Context, res dynamic.ResourceInterface, obj *unstructured.Unstructured, entry teamRegEntry) error {
	fresh, err := res.Get(ctx, obj.GetName(), metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("re-read Team %s for status: %w", obj.GetName(), err)
	}
	status := map[string]interface{}{
		"phase":          "Active",
		"teamRoomID":     entry.TeamRoomID,
		"leaderDMRoomID": entry.LeaderDMRoomID,
		"leaderReady":    true,
		"readyWorkers":   int64(len(entry.Workers)),
		"totalWorkers":   int64(len(entry.Workers)),
	}
	if err := unstructured.SetNestedField(fresh.Object, status, "status"); err != nil {
		return fmt.Errorf("set status on Team %s: %w", obj.GetName(), err)
	}
	_, err = res.UpdateStatus(ctx, fresh, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("update Team %s status: %w", obj.GetName(), err)
	}
	return nil
}

// --- Human CR ---

func (m *Migrator) createHumanCR(ctx context.Context, dynClient dynamic.Interface, name string, entry humanRegEntry) error {
	res := dynClient.Resource(humanGVR).Namespace(m.Namespace)

	if _, err := res.Get(ctx, name, metav1.GetOptions{}); err == nil {
		return nil
	}

	spec := map[string]interface{}{
		"displayName":     entry.DisplayName,
		"permissionLevel": int64(entry.PermissionLevel),
	}
	if len(entry.AccessibleTeams) > 0 {
		spec["accessibleTeams"] = toInterfaceSlice(entry.AccessibleTeams)
	}

	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": v1beta1.GroupName + "/" + v1beta1.Version,
			"kind":       "Human",
			"metadata": map[string]interface{}{
				"name":       name,
				"namespace":  m.Namespace,
				"finalizers": []interface{}{"hiclaw.io/cleanup"},
			},
			"spec": spec,
		},
	}

	created, err := res.Create(ctx, obj, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("create Human CR %s: %w", name, err)
	}
	return m.setHumanStatus(ctx, res, created, entry)
}

func (m *Migrator) setHumanStatus(ctx context.Context, res dynamic.ResourceInterface, obj *unstructured.Unstructured, entry humanRegEntry) error {
	fresh, err := res.Get(ctx, obj.GetName(), metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("re-read Human %s for status: %w", obj.GetName(), err)
	}
	status := map[string]interface{}{
		"phase":        "Active",
		"matrixUserID": entry.MatrixUserID,
	}
	if err := unstructured.SetNestedField(fresh.Object, status, "status"); err != nil {
		return fmt.Errorf("set status on Human %s: %w", obj.GetName(), err)
	}
	_, err = res.UpdateStatus(ctx, fresh, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("update Human %s status: %w", obj.GetName(), err)
	}
	return nil
}

func (m *Migrator) writeMarker(ctx context.Context) error {
	return m.OSS.PutObject(ctx, migrationMarker, []byte("migration completed"))
}

// --- Helpers ---

func toInterfaceSlice(ss []string) []interface{} {
	result := make([]interface{}, len(ss))
	for i, s := range ss {
		result[i] = s
	}
	return result
}

func appendUnique(slice []string, val string) []string {
	for _, v := range slice {
		if v == val {
			return slice
		}
	}
	return append(slice, val)
}
