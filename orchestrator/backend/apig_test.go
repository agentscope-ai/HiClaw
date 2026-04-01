package backend

import (
	"context"
	"fmt"
	"testing"

	apig "github.com/alibabacloud-go/apig-20240327/v6/client"
	"github.com/alibabacloud-go/tea/tea"
)

// mockAPIGClient implements APIGClient for testing.
type mockAPIGClient struct {
	consumers map[string]*mockConsumer // consumerID -> consumer
	rules     map[string][]string     // consumerID -> ruleIDs
	nextID    int
}

type mockConsumer struct {
	id     string
	name   string
	apiKey string
}

func newMockAPIGClient() *mockAPIGClient {
	return &mockAPIGClient{
		consumers: map[string]*mockConsumer{},
		rules:     map[string][]string{},
	}
}

func (m *mockAPIGClient) CreateConsumer(req *apig.CreateConsumerRequest) (*apig.CreateConsumerResponse, error) {
	name := tea.StringValue(req.Name)
	for _, c := range m.consumers {
		if c.name == name {
			return nil, fmt.Errorf("ConsumerNameDuplicate: %s", name)
		}
	}
	m.nextID++
	id := fmt.Sprintf("cs-%d", m.nextID)
	apiKey := fmt.Sprintf("key-%s", name)
	m.consumers[id] = &mockConsumer{id: id, name: name, apiKey: apiKey}
	return &apig.CreateConsumerResponse{
		Body: &apig.CreateConsumerResponseBody{
			Data: &apig.CreateConsumerResponseBodyData{
				ConsumerId: tea.String(id),
			},
		},
	}, nil
}

func (m *mockAPIGClient) GetConsumer(consumerId *string) (*apig.GetConsumerResponse, error) {
	id := tea.StringValue(consumerId)
	c, ok := m.consumers[id]
	if !ok {
		return nil, fmt.Errorf("consumer not found: %s", id)
	}
	return &apig.GetConsumerResponse{
		Body: &apig.GetConsumerResponseBody{
			Data: &apig.GetConsumerResponseBodyData{
				ConsumerId: tea.String(c.id),
				ApiKeyIdentityConfig: &apig.ApiKeyIdentityConfig{
					Credentials: []*apig.ApiKeyIdentityConfigCredentials{
						{Apikey: tea.String(c.apiKey)},
					},
				},
			},
		},
	}, nil
}

func (m *mockAPIGClient) DeleteConsumer(consumerId *string) (*apig.DeleteConsumerResponse, error) {
	id := tea.StringValue(consumerId)
	delete(m.consumers, id)
	delete(m.rules, id)
	return &apig.DeleteConsumerResponse{}, nil
}

func (m *mockAPIGClient) ListConsumers(req *apig.ListConsumersRequest) (*apig.ListConsumersResponse, error) {
	nameLike := tea.StringValue(req.NameLike)
	var items []*apig.ListConsumersResponseBodyDataItems
	for _, c := range m.consumers {
		if nameLike != "" && c.name != nameLike {
			continue
		}
		items = append(items, &apig.ListConsumersResponseBodyDataItems{
			ConsumerId: tea.String(c.id),
			Name:       tea.String(c.name),
		})
	}
	return &apig.ListConsumersResponse{
		Body: &apig.ListConsumersResponseBody{
			Data: &apig.ListConsumersResponseBodyData{
				Items: items,
			},
		},
	}, nil
}

func (m *mockAPIGClient) CreateConsumerAuthorizationRules(req *apig.CreateConsumerAuthorizationRulesRequest) (*apig.CreateConsumerAuthorizationRulesResponse, error) {
	var ruleIDs []*string
	for _, rule := range req.AuthorizationRules {
		cid := tea.StringValue(rule.ConsumerId)
		m.nextID++
		ruleID := fmt.Sprintf("rule-%d", m.nextID)
		m.rules[cid] = append(m.rules[cid], ruleID)
		ruleIDs = append(ruleIDs, tea.String(ruleID))
	}
	return &apig.CreateConsumerAuthorizationRulesResponse{
		Body: &apig.CreateConsumerAuthorizationRulesResponseBody{
			Data: &apig.CreateConsumerAuthorizationRulesResponseBodyData{
				ConsumerAuthorizationRuleIds: ruleIDs,
			},
		},
	}, nil
}

func (m *mockAPIGClient) QueryConsumerAuthorizationRules(req *apig.QueryConsumerAuthorizationRulesRequest) (*apig.QueryConsumerAuthorizationRulesResponse, error) {
	cid := tea.StringValue(req.ConsumerId)
	rules := m.rules[cid]
	var items []*apig.QueryConsumerAuthorizationRulesResponseBodyDataItems
	for _, rid := range rules {
		items = append(items, &apig.QueryConsumerAuthorizationRulesResponseBodyDataItems{
			ConsumerAuthorizationRuleId: tea.String(rid),
		})
	}
	return &apig.QueryConsumerAuthorizationRulesResponse{
		Body: &apig.QueryConsumerAuthorizationRulesResponseBody{
			Data: &apig.QueryConsumerAuthorizationRulesResponseBodyData{
				Items: items,
			},
		},
	}, nil
}

func newTestAPIGBackend(client APIGClient) *APIGBackend {
	return NewAPIGBackendWithClient(client, APIGConfig{
		Region:     "cn-hangzhou",
		GatewayID:  "gw-test",
		ModelAPIID: "api-test",
		EnvID:      "env-test",
	})
}

func TestAPIGCreateConsumer(t *testing.T) {
	mock := newMockAPIGClient()
	b := newTestAPIGBackend(mock)

	result, err := b.CreateConsumer(context.Background(), ConsumerRequest{Name: "alice"})
	if err != nil {
		t.Fatalf("CreateConsumer failed: %v", err)
	}
	if result.Status != "created" {
		t.Errorf("expected created, got %s", result.Status)
	}
	if result.ConsumerID == "" {
		t.Error("expected non-empty consumer ID")
	}
	if result.APIKey == "" {
		t.Error("expected non-empty API key")
	}
}

func TestAPIGCreateConsumerIdempotent(t *testing.T) {
	mock := newMockAPIGClient()
	b := newTestAPIGBackend(mock)

	b.CreateConsumer(context.Background(), ConsumerRequest{Name: "bob"})
	result, err := b.CreateConsumer(context.Background(), ConsumerRequest{Name: "bob"})
	if err != nil {
		t.Fatalf("second CreateConsumer failed: %v", err)
	}
	if result.Status != "exists" {
		t.Errorf("expected exists, got %s", result.Status)
	}
}

func TestAPIGBindConsumer(t *testing.T) {
	mock := newMockAPIGClient()
	b := newTestAPIGBackend(mock)

	result, _ := b.CreateConsumer(context.Background(), ConsumerRequest{Name: "carol"})

	err := b.BindConsumer(context.Background(), BindRequest{
		ConsumerID: result.ConsumerID,
		ModelAPIID: "api-test",
		EnvID:      "env-test",
	})
	if err != nil {
		t.Fatalf("BindConsumer failed: %v", err)
	}

	// Second bind should be idempotent
	err = b.BindConsumer(context.Background(), BindRequest{
		ConsumerID: result.ConsumerID,
		ModelAPIID: "api-test",
		EnvID:      "env-test",
	})
	if err != nil {
		t.Fatalf("second BindConsumer failed: %v", err)
	}
}

func TestAPIGDeleteConsumer(t *testing.T) {
	mock := newMockAPIGClient()
	b := newTestAPIGBackend(mock)

	result, _ := b.CreateConsumer(context.Background(), ConsumerRequest{Name: "dave"})

	err := b.DeleteConsumer(context.Background(), result.ConsumerID)
	if err != nil {
		t.Fatalf("DeleteConsumer failed: %v", err)
	}

	if len(mock.consumers) != 0 {
		t.Errorf("expected 0 consumers after delete, got %d", len(mock.consumers))
	}
}

func TestAPIGConsumerNamePrefix(t *testing.T) {
	mock := newMockAPIGClient()
	b := newTestAPIGBackend(mock)

	b.CreateConsumer(context.Background(), ConsumerRequest{Name: "eve"})

	// Verify the consumer was created with gateway ID prefix
	found := false
	for _, c := range mock.consumers {
		if c.name == "gw-test-eve" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected consumer name to be prefixed with gateway ID")
	}
}
