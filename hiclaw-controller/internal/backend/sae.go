package backend

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	openapi "github.com/alibabacloud-go/darabonba-openapi/v2/client"
	sae "github.com/alibabacloud-go/sae-20190506/v4/client"
)

// SAEClient abstracts the SAE SDK client for testability.
type SAEClient interface {
	CreateApplication(req *sae.CreateApplicationRequest) (*sae.CreateApplicationResponse, error)
	DeleteApplication(req *sae.DeleteApplicationRequest) (*sae.DeleteApplicationResponse, error)
	StartApplication(req *sae.StartApplicationRequest) (*sae.StartApplicationResponse, error)
	StopApplication(req *sae.StopApplicationRequest) (*sae.StopApplicationResponse, error)
	DescribeApplicationStatus(req *sae.DescribeApplicationStatusRequest) (*sae.DescribeApplicationStatusResponse, error)
	ListApplications(req *sae.ListApplicationsRequest) (*sae.ListApplicationsResponse, error)
}

// SAEConfig holds SAE backend configuration.
type SAEConfig struct {
	Region          string
	NamespaceID     string
	WorkerImage     string
	CopawWorkerImage string
	VPCID           string
	VSwitchID       string
	SecurityGroupID string
	CPU             int32
	Memory          int32
}

// SAEBackend manages worker lifecycle via Alibaba Cloud SAE.
type SAEBackend struct {
	client          SAEClient
	config          SAEConfig
	containerPrefix string
}

// NewSAEBackend creates a SAEBackend with auto-configured SDK client.
func NewSAEBackend(creds CloudCredentialProvider, config SAEConfig, containerPrefix string) (*SAEBackend, error) {
	cred, err := creds.GetCredential()
	if err != nil {
		return nil, fmt.Errorf("build SAE credentials: %w", err)
	}

	endpoint := fmt.Sprintf("sae.%s.aliyuncs.com", config.Region)
	apiConfig := &openapi.Config{}
	apiConfig.SetCredential(cred).
		SetRegionId(config.Region).
		SetEndpoint(endpoint)

	client, err := sae.NewClient(apiConfig)
	if err != nil {
		return nil, fmt.Errorf("create SAE client: %w", err)
	}

	return NewSAEBackendWithClient(client, config, containerPrefix), nil
}

// NewSAEBackendWithClient creates a SAEBackend with a custom client (for testing).
func NewSAEBackendWithClient(client SAEClient, config SAEConfig, containerPrefix string) *SAEBackend {
	if containerPrefix == "" {
		containerPrefix = DefaultContainerPrefix
	}
	if config.CPU == 0 {
		config.CPU = 1000
	}
	if config.Memory == 0 {
		config.Memory = 2048
	}
	return &SAEBackend{
		client:          client,
		config:          config,
		containerPrefix: containerPrefix,
	}
}

func (s *SAEBackend) Name() string                        { return "sae" }
func (s *SAEBackend) DeploymentMode() string               { return DeployCloud }
func (s *SAEBackend) NeedsCredentialInjection() bool       { return true }

func (s *SAEBackend) Available(_ context.Context) bool {
	return s.config.WorkerImage != "" && s.config.NamespaceID != ""
}

func (s *SAEBackend) Create(ctx context.Context, req CreateRequest) (*WorkerResult, error) {
	appName := s.containerPrefix + req.Name

	// Check if already exists
	existingID, err := s.findAppByName(appName)
	if err != nil {
		return nil, err
	}
	if existingID != "" {
		return nil, fmt.Errorf("%w: SAE app %q", ErrConflict, appName)
	}

	// Build env vars
	image := req.Image
	if image == "" {
		if req.Runtime == RuntimeCopaw && s.config.CopawWorkerImage != "" {
			image = s.config.CopawWorkerImage
		} else {
			image = s.config.WorkerImage
		}
	}

	// SAE backend auto-injects runtime and credentials into worker env
	if req.Env == nil {
		req.Env = make(map[string]string)
	}
	req.Env["HICLAW_RUNTIME"] = "aliyun"
	if req.AuthToken != "" {
		req.Env["HICLAW_AUTH_TOKEN"] = req.AuthToken
	}
	if req.ControllerURL != "" {
		req.Env["HICLAW_CONTROLLER_URL"] = req.ControllerURL
	}

	envList := s.buildEnvList(req.Env)

	saeReq := &sae.CreateApplicationRequest{}
	saeReq.SetAppName(appName).
		SetNamespaceId(s.config.NamespaceID).
		SetPackageType("Image").
		SetImageUrl(image).
		SetCpu(s.config.CPU).
		SetMemory(s.config.Memory).
		SetReplicas(1).
		SetVpcId(s.config.VPCID).
		SetVSwitchId(s.config.VSwitchID).
		SetSecurityGroupId(s.config.SecurityGroupID).
		SetAppDescription(fmt.Sprintf("HiClaw Worker Agent: %s", req.Name)).
		SetEnvs(envList).
		SetCustomImageNetworkType("internet")

	resp, err := s.client.CreateApplication(saeReq)
	if err != nil {
		return nil, fmt.Errorf("SAE CreateApplication: %w", err)
	}

	appID := ""
	if resp.Body != nil && resp.Body.Data != nil && resp.Body.Data.AppId != nil {
		appID = *resp.Body.Data.AppId
	}

	log.Printf("[SAE] Created application %s (%s), waiting for RUNNING...", appName, appID)

	// Poll DescribeApplicationStatus until RUNNING (max 120s)
	for elapsed := 0; elapsed < 120; elapsed += 5 {
		statusReq := &sae.DescribeApplicationStatusRequest{}
		statusReq.SetAppId(appID)
		statusResp, err := s.client.DescribeApplicationStatus(statusReq)
		if err == nil && statusResp.Body != nil && statusResp.Body.Data != nil &&
			statusResp.Body.Data.CurrentStatus != nil {
			current := *statusResp.Body.Data.CurrentStatus
			if current == "RUNNING" {
				log.Printf("[SAE] Application %s is RUNNING", appName)
				return &WorkerResult{
					Name:           req.Name,
					Backend:        "sae",
					DeploymentMode: DeployCloud,
					Status:         StatusRunning,
					AppID:          appID,
					RawStatus:      "RUNNING",
				}, nil
			}
			if strings.Contains(current, "FAILED") {
				return nil, fmt.Errorf("SAE application %s entered failed state: %s", appName, current)
			}
			log.Printf("[SAE] Application %s status: %s (%ds)", appName, current, elapsed)
		} else if err != nil {
			log.Printf("[SAE] DescribeApplicationStatus error for %s: %v", appName, err)
		}
		select {
		case <-ctx.Done():
			return &WorkerResult{
				Name:           req.Name,
				Backend:        "sae",
				DeploymentMode: DeployCloud,
				Status:         StatusStarting,
				AppID:          appID,
			}, nil
		case <-time.After(5 * time.Second):
		}
	}

	log.Printf("[SAE] Application %s did not reach RUNNING within 120s", appName)
	return &WorkerResult{
		Name:           req.Name,
		Backend:        "sae",
		DeploymentMode: DeployCloud,
		Status:         StatusStarting,
		AppID:          appID,
	}, nil
}

func (s *SAEBackend) Delete(_ context.Context, name string) error {
	appName := s.containerPrefix + name
	appID, err := s.findAppByName(appName)
	if err != nil {
		return err
	}
	if appID == "" {
		return nil // already gone
	}

	req := &sae.DeleteApplicationRequest{}
	req.SetAppId(appID)
	_, err = s.client.DeleteApplication(req)
	if err != nil {
		return fmt.Errorf("SAE DeleteApplication: %w", err)
	}

	log.Printf("[SAE] Deleted application %s (%s)", appName, appID)
	return nil
}

func (s *SAEBackend) Start(_ context.Context, name string) error {
	appName := s.containerPrefix + name
	appID, err := s.findAppByName(appName)
	if err != nil {
		return err
	}
	if appID == "" {
		return fmt.Errorf("%w: worker %q", ErrNotFound, name)
	}

	req := &sae.StartApplicationRequest{}
	req.SetAppId(appID)
	_, err = s.client.StartApplication(req)
	if err != nil {
		return fmt.Errorf("SAE StartApplication: %w", err)
	}
	return nil
}

func (s *SAEBackend) Stop(_ context.Context, name string) error {
	appName := s.containerPrefix + name
	appID, err := s.findAppByName(appName)
	if err != nil {
		return err
	}
	if appID == "" {
		return fmt.Errorf("%w: worker %q", ErrNotFound, name)
	}

	req := &sae.StopApplicationRequest{}
	req.SetAppId(appID)
	_, err = s.client.StopApplication(req)
	if err != nil {
		return fmt.Errorf("SAE StopApplication: %w", err)
	}
	return nil
}

func (s *SAEBackend) Status(_ context.Context, name string) (*WorkerResult, error) {
	appName := s.containerPrefix + name
	appID, err := s.findAppByName(appName)
	if err != nil {
		return nil, err
	}
	if appID == "" {
		return &WorkerResult{
			Name:           name,
			Backend:        "sae",
			DeploymentMode: DeployCloud,
			Status:         StatusNotFound,
		}, nil
	}

	req := &sae.DescribeApplicationStatusRequest{}
	req.SetAppId(appID)
	resp, err := s.client.DescribeApplicationStatus(req)
	if err != nil {
		return nil, fmt.Errorf("SAE DescribeApplicationStatus: %w", err)
	}

	rawStatus := "unknown"
	if resp.Body != nil && resp.Body.Data != nil && resp.Body.Data.CurrentStatus != nil {
		rawStatus = *resp.Body.Data.CurrentStatus
	}

	return &WorkerResult{
		Name:           name,
		Backend:        "sae",
		DeploymentMode: DeployCloud,
		Status:         normalizeSAEStatus(rawStatus),
		AppID:          appID,
		RawStatus:      rawStatus,
	}, nil
}

func (s *SAEBackend) List(_ context.Context) ([]WorkerResult, error) {
	req := &sae.ListApplicationsRequest{}
	req.SetNamespaceId(s.config.NamespaceID)
	resp, err := s.client.ListApplications(req)
	if err != nil {
		return nil, fmt.Errorf("SAE ListApplications: %w", err)
	}

	results := make([]WorkerResult, 0)
	if resp.Body == nil || resp.Body.Data == nil {
		return results, nil
	}

	for _, app := range resp.Body.Data.Applications {
		if app.AppName == nil || !strings.HasPrefix(*app.AppName, s.containerPrefix) {
			continue
		}
		name := strings.TrimPrefix(*app.AppName, s.containerPrefix)
		appID := ""
		if app.AppId != nil {
			appID = *app.AppId
		}
		results = append(results, WorkerResult{
			Name:           name,
			Backend:        "sae",
			DeploymentMode: DeployCloud,
			AppID:          appID,
		})
	}
	return results, nil
}

// --- internal helpers ---

func (s *SAEBackend) findAppByName(appName string) (string, error) {
	req := &sae.ListApplicationsRequest{}
	req.SetNamespaceId(s.config.NamespaceID).
		SetAppName(appName)
	resp, err := s.client.ListApplications(req)
	if err != nil {
		return "", fmt.Errorf("SAE ListApplications: %w", err)
	}

	if resp.Body == nil || resp.Body.Data == nil {
		return "", nil
	}

	for _, app := range resp.Body.Data.Applications {
		if app.AppName != nil && *app.AppName == appName {
			if app.AppId != nil {
				return *app.AppId, nil
			}
		}
	}
	return "", nil
}

func (s *SAEBackend) buildEnvList(env map[string]string) string {
	type envEntry struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}
	entries := make([]envEntry, 0, len(env))
	for k, v := range env {
		entries = append(entries, envEntry{Name: k, Value: v})
	}
	b, _ := json.Marshal(entries)
	return string(b)
}

func normalizeSAEStatus(status string) WorkerStatus {
	switch strings.ToUpper(status) {
	case "RUNNING":
		return StatusRunning
	case "STOPPED":
		return StatusStopped
	case "DEPLOYING":
		return StatusStarting
	default:
		return StatusUnknown
	}
}
