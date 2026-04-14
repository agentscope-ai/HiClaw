package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	v1beta1 "github.com/hiclaw/hiclaw-controller/api/v1beta1"
	"github.com/hiclaw/hiclaw-controller/internal/agentconfig"
	"github.com/hiclaw/hiclaw-controller/internal/executor"
	"github.com/hiclaw/hiclaw-controller/internal/oss"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// --- Request types ---

// WorkerDeployRequest describes a worker config deployment (create or update).
type WorkerDeployRequest struct {
	Name           string
	Spec           v1beta1.WorkerSpec
	Role           string // "standalone" | "team_leader" | "worker"
	TeamName       string
	TeamLeaderName string

	// From provisioning
	MatrixToken    string
	GatewayKey     string
	MatrixPassword string
	AuthorizedMCPs []string

	TeamAdminMatrixID string

	IsUpdate bool
}

// CoordinationDeployRequest describes coordination context injection for a team leader.
type CoordinationDeployRequest struct {
	LeaderName        string
	Role              string
	TeamName          string
	TeamRoomID        string
	LeaderDMRoomID    string
	HeartbeatEvery    string
	WorkerIdleTimeout string
	TeamWorkers       []string
	TeamAdminID       string
}

// --- Deployer ---

// DeployerConfig holds configuration for constructing a Deployer.
type DeployerConfig struct {
	AgentConfig    *agentconfig.Generator
	OSS            oss.StorageClient
	Executor       *executor.Shell
	Packages       *executor.PackageResolver
	Legacy         *LegacyCompat
	AgentFSDir     string // embedded: /root/hiclaw-fs/agents
	WorkerAgentDir string // source for builtin agent files
	MatrixDomain   string
}

// Deployer orchestrates configuration deployment for workers: package resolution,
// inline config writes, openclaw.json generation, AGENTS.md merging, skill pushing,
// and OSS synchronization.
type Deployer struct {
	agentConfig    *agentconfig.Generator
	oss            oss.StorageClient
	executor       *executor.Shell
	packages       *executor.PackageResolver
	legacy         *LegacyCompat
	agentFSDir     string
	workerAgentDir string
	matrixDomain   string
}

func NewDeployer(cfg DeployerConfig) *Deployer {
	return &Deployer{
		agentConfig:    cfg.AgentConfig,
		oss:            cfg.OSS,
		executor:       cfg.Executor,
		packages:       cfg.Packages,
		legacy:         cfg.Legacy,
		agentFSDir:     cfg.AgentFSDir,
		workerAgentDir: cfg.WorkerAgentDir,
		matrixDomain:   cfg.MatrixDomain,
	}
}

// DeployPackage resolves, downloads, extracts, and deploys a package to OSS.
// No-op if uri is empty.
func (d *Deployer) DeployPackage(ctx context.Context, name, uri string, isUpdate bool) error {
	if uri == "" || d.packages == nil {
		return nil
	}

	extractedDir, err := d.packages.ResolveAndExtract(ctx, uri, name)
	if err != nil {
		return fmt.Errorf("package resolve/extract failed: %w", err)
	}
	if extractedDir == "" {
		return nil
	}

	if err := d.packages.DeployToMinIO(ctx, extractedDir, name, isUpdate); err != nil {
		return fmt.Errorf("package deploy failed: %w", err)
	}

	return nil
}

// WriteInlineConfigs writes inline identity/soul/agents content to the local agent directory.
// No-op if all inline fields are empty.
func (d *Deployer) WriteInlineConfigs(name string, spec v1beta1.WorkerSpec) error {
	if spec.Identity == "" && spec.Soul == "" && spec.Agents == "" {
		return nil
	}
	agentDir := fmt.Sprintf("%s/%s", d.agentFSDir, name)
	if err := executor.WriteInlineConfigs(agentDir, spec.Runtime, spec.Identity, spec.Soul, spec.Agents); err != nil {
		return err
	}
	log.Log.Info("inline configs written", "name", name)
	return nil
}

// DeployWorkerConfig generates and pushes all configuration files to OSS:
// openclaw.json, SOUL.md, mcporter config, Matrix password, agent file sync,
// AGENTS.md merge with builtin section + coordination context, builtin skills.
func (d *Deployer) DeployWorkerConfig(ctx context.Context, req WorkerDeployRequest) error {
	logger := log.FromContext(ctx)
	agentPrefix := fmt.Sprintf("agents/%s", req.Name)
	localAgentDir := fmt.Sprintf("%s/%s", d.agentFSDir, req.Name)

	// --- Sync local agent files to storage FIRST (base layer) ---
	// Mirror provides the base: package files, memory, custom skills, etc.
	// All subsequent PutObject calls overwrite on top with authoritative content.
	// Exclude files that will be overridden by inline spec content to prevent
	// the mirror from pushing stale local copies that race with PutObject.
	logger.Info("syncing agent files to storage", "name", req.Name)
	var mirrorExcludes []string
	if req.Spec.Soul != "" {
		mirrorExcludes = append(mirrorExcludes, "SOUL.md")
	}
	if req.Spec.Agents != "" {
		mirrorExcludes = append(mirrorExcludes, "AGENTS.md")
	}
	if err := d.oss.Mirror(ctx, localAgentDir+"/", agentPrefix+"/", oss.MirrorOptions{Overwrite: true, Exclude: mirrorExcludes}); err != nil {
		logger.Error(err, "agent file sync failed (non-fatal)")
	}

	// --- openclaw.json ---
	var channelPolicy *agentconfig.ChannelPolicy
	if req.Spec.ChannelPolicy != nil {
		channelPolicy = &agentconfig.ChannelPolicy{
			GroupAllowExtra: req.Spec.ChannelPolicy.GroupAllowExtra,
			GroupDenyExtra:  req.Spec.ChannelPolicy.GroupDenyExtra,
			DMAllowExtra:    req.Spec.ChannelPolicy.DmAllowExtra,
			DMDenyExtra:     req.Spec.ChannelPolicy.DmDenyExtra,
		}
	}

	configJSON, err := d.agentConfig.GenerateOpenClawConfig(agentconfig.WorkerConfigRequest{
		WorkerName:     req.Name,
		MatrixToken:    req.MatrixToken,
		GatewayKey:     req.GatewayKey,
		ModelName:      req.Spec.Model,
		TeamLeaderName: req.TeamLeaderName,
		ChannelPolicy:  channelPolicy,
	})
	if err != nil {
		return fmt.Errorf("config generation failed: %w", err)
	}
	if err := d.oss.PutObject(ctx, agentPrefix+"/openclaw.json", configJSON); err != nil {
		return fmt.Errorf("config push to storage failed: %w", err)
	}

	// --- SOUL.md ---
	// Priority: inline spec (user intent) > local file (from package) > generated default.
	// Inline spec is read directly from memory to avoid local file race with background mc mirror.
	soulPath := filepath.Join(localAgentDir, "SOUL.md")
	if req.Spec.Soul != "" {
		if err := d.oss.PutObject(ctx, agentPrefix+"/SOUL.md", []byte(req.Spec.Soul)); err != nil {
			logger.Error(err, "SOUL.md push failed (non-fatal)")
		}
	} else if soulData, err := os.ReadFile(soulPath); err == nil {
		if err := d.oss.PutObject(ctx, agentPrefix+"/SOUL.md", soulData); err != nil {
			logger.Error(err, "SOUL.md push failed (non-fatal)")
		}
	} else if !req.IsUpdate {
		soulContent := fmt.Sprintf("# %s\n\nYou are %s, an AI worker agent.\n", req.Name, req.Name)
		if err := d.oss.PutObject(ctx, agentPrefix+"/SOUL.md", []byte(soulContent)); err != nil {
			logger.Error(err, "SOUL.md push failed (non-fatal)")
		}
	}

	// --- mcporter-servers.json ---
	if len(req.AuthorizedMCPs) > 0 {
		mcporterJSON, err := d.agentConfig.GenerateMcporterConfig(req.GatewayKey, "", req.AuthorizedMCPs)
		if err != nil {
			logger.Error(err, "mcporter config generation failed (non-fatal)")
		} else if mcporterJSON != nil {
			if err := d.oss.PutObject(ctx, agentPrefix+"/mcporter-servers.json", mcporterJSON); err != nil {
				logger.Error(err, "mcporter config push failed (non-fatal)")
			}
		}
	}

	// --- Matrix password to storage for E2EE re-login ---
	if req.MatrixPassword != "" {
		if err := d.oss.PutObject(ctx, agentPrefix+"/credentials/matrix/password", []byte(req.MatrixPassword)); err != nil {
			logger.Error(err, "failed to write Matrix password to storage (non-fatal)")
		}
	}

	// --- Builtin top-level files (e.g. HEARTBEAT.md for team leaders) ---
	if err := d.pushBuiltinTopLevelFiles(ctx, req.Name, agentPrefix, req.Role); err != nil {
		logger.Error(err, "builtin top-level file sync failed (non-fatal)")
	}

	// --- AGENTS.md: merge builtin section + inject coordination context ---
	if err := d.prepareAndPushAgentsMD(ctx, req.Name, agentPrefix, req.Role, req.TeamName, req.TeamLeaderName, req.TeamAdminMatrixID, req.Spec.Agents); err != nil {
		logger.Error(err, "AGENTS.md prepare failed (non-fatal)")
	}

	// --- Push builtin skills from worker-agent template ---
	if err := d.pushBuiltinSkills(ctx, req.Name, agentPrefix, req.Role); err != nil {
		logger.Error(err, "builtin skills push failed (non-fatal)")
	}

	return nil
}

// InjectCoordinationContext writes team coordination context into the leader's AGENTS.md.
func (d *Deployer) InjectCoordinationContext(ctx context.Context, req CoordinationDeployRequest) error {
	leaderAgentPrefix := fmt.Sprintf("agents/%s", req.LeaderName)

	teamWorkers := make([]agentconfig.TeamWorkerInfo, 0, len(req.TeamWorkers))
	for _, wn := range req.TeamWorkers {
		teamWorkers = append(teamWorkers, agentconfig.TeamWorkerInfo{Name: wn})
	}

	coordCtx := agentconfig.CoordinationContext{
		WorkerName:        req.LeaderName,
		Role:              req.Role,
		MatrixDomain:      d.matrixDomain,
		TeamName:          req.TeamName,
		TeamRoomID:        req.TeamRoomID,
		LeaderDMRoomID:    req.LeaderDMRoomID,
		HeartbeatEvery:    req.HeartbeatEvery,
		WorkerIdleTimeout: req.WorkerIdleTimeout,
		TeamWorkers:       teamWorkers,
		TeamAdminID:       req.TeamAdminID,
	}

	existing, _ := d.oss.GetObject(ctx, leaderAgentPrefix+"/AGENTS.md")
	injected := agentconfig.InjectCoordinationContext(string(existing), coordCtx)
	return d.oss.PutObject(ctx, leaderAgentPrefix+"/AGENTS.md", []byte(injected))
}

// PushOnDemandSkills runs the push-worker-skills.sh script for on-demand skills.
// No-op if no skills or executor is nil.
func (d *Deployer) PushOnDemandSkills(ctx context.Context, workerName string, skills []string) error {
	if len(skills) == 0 || d.executor == nil {
		return nil
	}
	_, err := d.executor.RunSimple(ctx,
		"/opt/hiclaw/agent/skills/worker-management/scripts/push-worker-skills.sh",
		"--worker", workerName, "--no-notify",
	)
	return err
}

// CleanupOSSData removes all agent data from OSS for a deleted worker.
func (d *Deployer) CleanupOSSData(ctx context.Context, workerName string) error {
	agentPrefix := fmt.Sprintf("agents/%s/", workerName)
	return d.oss.DeletePrefix(ctx, agentPrefix)
}

// EnsureTeamStorage creates the shared storage directories for a team.
func (d *Deployer) EnsureTeamStorage(ctx context.Context, teamName string) error {
	prefix := fmt.Sprintf("teams/%s/", teamName)
	for _, subdir := range []string{"shared/tasks/", "shared/projects/", "shared/knowledge/"} {
		if err := d.oss.PutObject(ctx, prefix+subdir+".keep", []byte("")); err != nil {
			return fmt.Errorf("create %s%s: %w", prefix, subdir, err)
		}
	}
	return nil
}

// --- Manager Config Deployment ---

// ManagerDeployRequest describes a Manager config deployment (create or update).
type ManagerDeployRequest struct {
	Name           string
	Spec           v1beta1.ManagerSpec
	MatrixToken    string
	GatewayKey     string
	MatrixPassword string
	AuthorizedMCPs []string
	IsUpdate       bool
}

// DeployManagerConfig generates and pushes Manager configuration files to OSS.
// Unlike Worker, AGENTS.md and builtin skills are managed by the Manager container
// itself (via upgrade-builtins.sh), so we only push runtime-generated files.
func (d *Deployer) DeployManagerConfig(ctx context.Context, req ManagerDeployRequest) error {
	logger := log.FromContext(ctx)
	agentPrefix := fmt.Sprintf("agents/%s", req.Name)

	// --- openclaw.json ---
	configJSON, err := d.agentConfig.GenerateOpenClawConfig(agentconfig.WorkerConfigRequest{
		WorkerName:  req.Name,
		MatrixToken: req.MatrixToken,
		GatewayKey:  req.GatewayKey,
		ModelName:   req.Spec.Model,
	})
	if err != nil {
		return fmt.Errorf("config generation failed: %w", err)
	}
	// Use LegacyCompat to write Manager config with mutex protection,
	// merging groupAllowFrom to avoid overwriting team leader additions.
	if d.legacy != nil && d.legacy.Enabled() {
		if err := d.legacy.PutManagerConfig(configJSON); err != nil {
			return fmt.Errorf("config push to storage failed: %w", err)
		}
	} else {
		if err := d.oss.PutObject(ctx, agentPrefix+"/openclaw.json", configJSON); err != nil {
			return fmt.Errorf("config push to storage failed: %w", err)
		}
	}

	// --- SOUL.md (only if explicitly set in CRD spec) ---
	if req.Spec.Soul != "" {
		if err := d.oss.PutObject(ctx, agentPrefix+"/SOUL.md", []byte(req.Spec.Soul)); err != nil {
			logger.Error(err, "SOUL.md push failed (non-fatal)")
		}
	}

	// --- AGENTS.md (only if explicitly set in CRD spec) ---
	if req.Spec.Agents != "" {
		if err := d.oss.PutObject(ctx, agentPrefix+"/AGENTS.md", []byte(req.Spec.Agents)); err != nil {
			logger.Error(err, "AGENTS.md push failed (non-fatal)")
		}
	}

	// --- mcporter-servers.json ---
	if len(req.AuthorizedMCPs) > 0 {
		mcporterJSON, err := d.agentConfig.GenerateMcporterConfig(req.GatewayKey, "", req.AuthorizedMCPs)
		if err != nil {
			logger.Error(err, "mcporter config generation failed (non-fatal)")
		} else if mcporterJSON != nil {
			if err := d.oss.PutObject(ctx, agentPrefix+"/mcporter-servers.json", mcporterJSON); err != nil {
				logger.Error(err, "mcporter config push failed (non-fatal)")
			}
		}
	}

	// --- Matrix password for E2EE re-login ---
	if req.MatrixPassword != "" {
		if err := d.oss.PutObject(ctx, agentPrefix+"/credentials/matrix/password", []byte(req.MatrixPassword)); err != nil {
			logger.Error(err, "failed to write Matrix password to storage (non-fatal)")
		}
	}

	return nil
}

// --- Internal helpers ---

// prepareAndPushAgentsMD merges the builtin AGENTS.md section and injects
// coordination context in a single OSS read-write cycle.
func (d *Deployer) prepareAndPushAgentsMD(ctx context.Context, workerName, agentPrefix, role, teamName, teamLeaderName, teamAdminMatrixID, inlineAgents string) error {
	builtinPath := filepath.Join(d.builtinAgentDir(role), "AGENTS.md")
	builtinContent, err := os.ReadFile(builtinPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read builtin AGENTS.md: %w", err)
	}

	// Priority: inline spec (user intent) > OSS (from package).
	// Read inline directly from memory to avoid local file race with background mc mirror.
	var content string
	if inlineAgents != "" {
		content = inlineAgents
	} else {
		existing, _ := d.oss.GetObject(ctx, agentPrefix+"/AGENTS.md")
		content = string(existing)
	}
	if len(builtinContent) > 0 {
		content = agentconfig.MergeBuiltinSection(content, string(builtinContent))
	}

	coordCtx := agentconfig.CoordinationContext{
		WorkerName:     workerName,
		MatrixDomain:   d.matrixDomain,
		TeamName:       teamName,
		TeamLeaderName: teamLeaderName,
		TeamAdminID:    teamAdminMatrixID,
	}
	if role == "team_leader" {
		coordCtx.Role = "team_leader"
	} else if teamLeaderName != "" {
		coordCtx.Role = "worker"
	} else {
		coordCtx.Role = "standalone"
	}
	content = agentconfig.InjectCoordinationContext(content, coordCtx)

	return d.oss.PutObject(ctx, agentPrefix+"/AGENTS.md", []byte(content))
}

// pushBuiltinSkills copies builtin skill directories from the worker-agent template to OSS.
func (d *Deployer) pushBuiltinSkills(ctx context.Context, workerName, agentPrefix, role string) error {
	skillsDir := filepath.Join(d.builtinAgentDir(role), "skills")
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		skillName := entry.Name()
		src := skillsDir + "/" + skillName + "/"
		dst := agentPrefix + "/skills/" + skillName + "/"
		if err := d.oss.Mirror(ctx, src, dst, oss.MirrorOptions{Overwrite: true}); err != nil {
			return fmt.Errorf("push skill %s: %w", skillName, err)
		}
	}
	return nil
}

func (d *Deployer) pushBuiltinTopLevelFiles(ctx context.Context, workerName, agentPrefix, role string) error {
	agentDir := d.builtinAgentDir(role)
	for _, name := range []string{"HEARTBEAT.md"} {
		src := filepath.Join(agentDir, name)
		content, err := os.ReadFile(src)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		if err := d.oss.PutObject(ctx, agentPrefix+"/"+name, content); err != nil {
			return err
		}
	}
	return nil
}

func (d *Deployer) builtinAgentDir(role string) string {
	baseDir := filepath.Dir(d.workerAgentDir)
	switch role {
	case "team_leader":
		return filepath.Join(baseDir, "team-leader-agent")
	default:
		return d.workerAgentDir
	}
}
