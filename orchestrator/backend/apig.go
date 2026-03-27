package backend

import (
	"context"
	"fmt"
	"log"
	"strings"

	openapi "github.com/alibabacloud-go/darabonba-openapi/v2/client"
	apig "github.com/alibabacloud-go/apig-20240327/v6/client"
	"github.com/alibabacloud-go/tea/tea"
)

// APIGClient abstracts the APIG SDK client for testability.
type APIGClient interface {
	CreateConsumer(req *apig.CreateConsumerRequest) (*apig.CreateConsumerResponse, error)
	GetConsumer(consumerId *string) (*apig.GetConsumerResponse, error)
	DeleteConsumer(consumerId *string) (*apig.DeleteConsumerResponse, error)
	ListConsumers(req *apig.ListConsumersRequest) (*apig.ListConsumersResponse, error)
	CreateConsumerAuthorizationRules(req *apig.CreateConsumerAuthorizationRulesRequest) (*apig.CreateConsumerAuthorizationRulesResponse, error)
	QueryConsumerAuthorizationRules(req *apig.QueryConsumerAuthorizationRulesRequest) (*apig.QueryConsumerAuthorizationRulesResponse, error)
}

// APIGConfig holds APIG backend configuration.
type APIGConfig struct {
	Region     string
	GatewayID  string
	ModelAPIID string
	EnvID      string
}

// APIGBackend manages AI Gateway consumers via Alibaba Cloud APIG.
type APIGBackend struct {
	client APIGClient
	config APIGConfig
}

// NewAPIGBackend creates an APIGBackend with auto-configured SDK client.
func NewAPIGBackend(creds CloudCredentialProvider, config APIGConfig) (*APIGBackend, error) {
	cred, err := creds.GetCredential()
	if err != nil {
		return nil, fmt.Errorf("build APIG credentials: %w", err)
	}

	endpoint := fmt.Sprintf("apig.%s.aliyuncs.com", config.Region)
	apiConfig := &openapi.Config{}
	apiConfig.SetCredential(cred).
		SetRegionId(config.Region).
		SetEndpoint(endpoint)

	client, err := apig.NewClient(apiConfig)
	if err != nil {
		return nil, fmt.Errorf("create APIG client: %w", err)
	}

	return &APIGBackend{client: client, config: config}, nil
}

// NewAPIGBackendWithClient creates an APIGBackend with a custom client (for testing).
func NewAPIGBackendWithClient(client APIGClient, config APIGConfig) *APIGBackend {
	return &APIGBackend{client: client, config: config}
}

func (a *APIGBackend) Name() string { return "apig" }

func (a *APIGBackend) Available(_ context.Context) bool {
	return a.config.GatewayID != ""
}

func (a *APIGBackend) CreateConsumer(_ context.Context, req ConsumerRequest) (*ConsumerResult, error) {
	// Prefix consumer name with gateway ID to avoid cross-gateway collisions
	consumerName := req.Name
	if a.config.GatewayID != "" {
		consumerName = a.config.GatewayID + "-" + req.Name
	}

	// Check if already exists
	existingID, existingKey, err := a.findConsumer(consumerName)
	if err != nil {
		return nil, err
	}
	if existingID != "" {
		return &ConsumerResult{
			Name:       req.Name,
			ConsumerID: existingID,
			APIKey:     existingKey,
			Status:     "exists",
		}, nil
	}

	// Create consumer
	createReq := &apig.CreateConsumerRequest{}
	createReq.SetName(consumerName).
		SetGatewayType("AI").
		SetEnable(true).
		SetDescription(fmt.Sprintf("HiClaw Worker: %s", req.Name)).
		SetApikeyIdentityConfig(&apig.ApiKeyIdentityConfig{
			Type: tea.String("Apikey"),
			ApikeySource: &apig.ApiKeyIdentityConfigApikeySource{
				Source: tea.String("Default"),
				Value:  tea.String("Authorization"),
			},
			Credentials: []*apig.ApiKeyIdentityConfigCredentials{
				{GenerateMode: tea.String("System")},
			},
		})

	resp, err := a.client.CreateConsumer(createReq)
	if err != nil {
		// Handle 409 race condition
		if strings.Contains(err.Error(), "ConsumerNameDuplicate") || strings.Contains(err.Error(), "409") {
			log.Printf("[APIG] Consumer creation returned 409, re-querying...")
			existingID, existingKey, err = a.findConsumer(consumerName)
			if err != nil {
				return nil, err
			}
			if existingID != "" {
				return &ConsumerResult{
					Name:       req.Name,
					ConsumerID: existingID,
					APIKey:     existingKey,
					Status:     "exists",
				}, nil
			}
			return nil, fmt.Errorf("consumer 409 but not found on re-query")
		}
		return nil, fmt.Errorf("APIG CreateConsumer: %w", err)
	}

	consumerID := ""
	if resp.Body != nil && resp.Body.Data != nil && resp.Body.Data.ConsumerId != nil {
		consumerID = *resp.Body.Data.ConsumerId
	}

	// Fetch API key from detail
	apiKey, err := a.getConsumerAPIKey(consumerID)
	if err != nil {
		log.Printf("[APIG] Warning: created consumer %s but failed to get API key: %v", consumerID, err)
	}

	log.Printf("[APIG] Created consumer %s (%s)", consumerName, consumerID)

	return &ConsumerResult{
		Name:       req.Name,
		ConsumerID: consumerID,
		APIKey:     apiKey,
		Status:     "created",
	}, nil
}

func (a *APIGBackend) BindConsumer(_ context.Context, req BindRequest) error {
	// Fallback to config if not provided in request
	modelAPIID := req.ModelAPIID
	if modelAPIID == "" {
		modelAPIID = a.config.ModelAPIID
	}
	envID := req.EnvID
	if envID == "" {
		envID = a.config.EnvID
	}
	if modelAPIID == "" || envID == "" {
		return fmt.Errorf("model_api_id and env_id are required (neither provided in request nor configured)")
	}

	// Check if already bound
	queryReq := &apig.QueryConsumerAuthorizationRulesRequest{}
	queryReq.SetConsumerId(req.ConsumerID).
		SetResourceId(modelAPIID).
		SetEnvironmentId(envID).
		SetResourceType("LLM").
		SetPageNumber(1).
		SetPageSize(100)

	queryResp, err := a.client.QueryConsumerAuthorizationRules(queryReq)
	if err == nil && queryResp.Body != nil && queryResp.Body.Data != nil &&
		queryResp.Body.Data.Items != nil && len(queryResp.Body.Data.Items) > 0 {
		log.Printf("[APIG] Consumer %s already bound (%d rules)", req.ConsumerID, len(queryResp.Body.Data.Items))
		return nil
	}

	// Create authorization rule
	createReq := &apig.CreateConsumerAuthorizationRulesRequest{}
	createReq.SetAuthorizationRules([]*apig.CreateConsumerAuthorizationRulesRequestAuthorizationRules{
		{
			ConsumerId:   tea.String(req.ConsumerID),
			ResourceType: tea.String("LLM"),
			ExpireMode:   tea.String("LongTerm"),
			ResourceIdentifier: &apig.CreateConsumerAuthorizationRulesRequestAuthorizationRulesResourceIdentifier{
				ResourceId:    tea.String(modelAPIID),
				EnvironmentId: tea.String(envID),
			},
		},
	})

	_, err = a.client.CreateConsumerAuthorizationRules(createReq)
	if err != nil {
		return fmt.Errorf("APIG CreateConsumerAuthorizationRules: %w", err)
	}

	log.Printf("[APIG] Consumer %s bound to API %s", req.ConsumerID, req.ModelAPIID)
	return nil
}

func (a *APIGBackend) DeleteConsumer(_ context.Context, consumerID string) error {
	_, err := a.client.DeleteConsumer(tea.String(consumerID))
	if err != nil {
		return fmt.Errorf("APIG DeleteConsumer: %w", err)
	}
	log.Printf("[APIG] Deleted consumer %s", consumerID)
	return nil
}

// --- internal helpers ---

func (a *APIGBackend) findConsumer(consumerName string) (string, string, error) {
	page := int32(1)
	for {
		req := &apig.ListConsumersRequest{}
		req.SetGatewayType("AI").
			SetNameLike(consumerName).
			SetPageNumber(page).
			SetPageSize(100)

		resp, err := a.client.ListConsumers(req)
		if err != nil {
			return "", "", fmt.Errorf("APIG ListConsumers: %w", err)
		}

		if resp.Body == nil || resp.Body.Data == nil || resp.Body.Data.Items == nil {
			break
		}

		for _, c := range resp.Body.Data.Items {
			if c.Name != nil && *c.Name == consumerName {
				consumerID := ""
				if c.ConsumerId != nil {
					consumerID = *c.ConsumerId
				}
				apiKey, _ := a.getConsumerAPIKey(consumerID)
				return consumerID, apiKey, nil
			}
		}

		if len(resp.Body.Data.Items) < 100 {
			break
		}
		page++
	}
	return "", "", nil
}

func (a *APIGBackend) getConsumerAPIKey(consumerID string) (string, error) {
	resp, err := a.client.GetConsumer(tea.String(consumerID))
	if err != nil {
		return "", err
	}
	if resp.Body != nil && resp.Body.Data != nil &&
		resp.Body.Data.ApiKeyIdentityConfig != nil &&
		resp.Body.Data.ApiKeyIdentityConfig.Credentials != nil &&
		len(resp.Body.Data.ApiKeyIdentityConfig.Credentials) > 0 {
		cred := resp.Body.Data.ApiKeyIdentityConfig.Credentials[0]
		if cred.Apikey != nil {
			return *cred.Apikey, nil
		}
	}
	return "", nil
}
