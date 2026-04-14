package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEnsureConsumer_Created(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/system/init":
			w.WriteHeader(http.StatusOK)
		case "/session/login":
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "test"})
			w.WriteHeader(http.StatusOK)
		case "/v1/consumers":
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	c := NewHigressClient(Config{
		ConsoleURL:    server.URL,
		AdminUser:     "admin",
		AdminPassword: "admin",
	}, server.Client())

	result, err := c.EnsureConsumer(context.Background(), ConsumerRequest{
		Name:          "worker-alice",
		CredentialKey: "key-abc-123",
	})
	if err != nil {
		t.Fatalf("EnsureConsumer: %v", err)
	}
	if result.Status != "created" {
		t.Errorf("Status = %q, want created", result.Status)
	}
	if result.APIKey != "key-abc-123" {
		t.Errorf("APIKey = %q, want key-abc-123", result.APIKey)
	}
}

func TestEnsureConsumer_Exists(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/system/init":
			w.WriteHeader(http.StatusOK)
		case "/session/login":
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "test"})
			w.WriteHeader(http.StatusOK)
		case "/v1/consumers":
			w.WriteHeader(http.StatusConflict)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	c := NewHigressClient(Config{ConsoleURL: server.URL}, server.Client())
	result, err := c.EnsureConsumer(context.Background(), ConsumerRequest{
		Name:          "worker-bob",
		CredentialKey: "key-xyz",
	})
	if err != nil {
		t.Fatalf("EnsureConsumer: %v", err)
	}
	if result.Status != "exists" {
		t.Errorf("Status = %q, want exists", result.Status)
	}
}

func TestAuthorizeAIRoutes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/session/login":
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "test"})
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == "/v1/ai/routes" && r.Method == "GET":
			json.NewEncoder(w).Encode(map[string]interface{}{
				"data": []map[string]interface{}{
					{"name": "route-1"},
				},
			})
		case r.URL.Path == "/v1/ai/routes/route-1" && r.Method == "GET":
			json.NewEncoder(w).Encode(map[string]interface{}{
				"data": map[string]interface{}{
					"name": "route-1",
					"authConfig": map[string]interface{}{
						"allowedConsumers": []string{"manager"},
					},
				},
			})
		case r.URL.Path == "/v1/ai/routes/route-1" && r.Method == "PUT":
			var body map[string]interface{}
			json.NewDecoder(r.Body).Decode(&body)
			authConfig, _ := body["authConfig"].(map[string]interface{})
			consumers := toStringSlice(authConfig["allowedConsumers"])
			if !containsString(consumers, "worker-alice") {
				t.Errorf("expected worker-alice in allowedConsumers, got %v", consumers)
			}
			w.WriteHeader(http.StatusOK)
		default:
			t.Logf("unexpected: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer server.Close()

	c := NewHigressClient(Config{ConsoleURL: server.URL}, server.Client())
	if err := c.AuthorizeAIRoutes(context.Background(), "worker-alice"); err != nil {
		t.Fatalf("AuthorizeAIRoutes: %v", err)
	}
}

func TestExposePort(t *testing.T) {
	var calledDomain, calledSvcSrc, calledRoute bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/session/login":
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "test"})
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == "/v1/domains":
			calledDomain = true
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == "/v1/service-sources":
			calledSvcSrc = true
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == "/v1/routes":
			calledRoute = true
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer server.Close()

	c := NewHigressClient(Config{ConsoleURL: server.URL}, server.Client())
	err := c.ExposePort(context.Background(), PortExposeRequest{
		WorkerName: "alice",
		Port:       3000,
	})
	if err != nil {
		t.Fatalf("ExposePort: %v", err)
	}
	if !calledDomain || !calledSvcSrc || !calledRoute {
		t.Errorf("expected all three Higress APIs called: domain=%v svcSrc=%v route=%v",
			calledDomain, calledSvcSrc, calledRoute)
	}
}

func TestSessionReauth(t *testing.T) {
	loginCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/system/init":
			w.WriteHeader(http.StatusOK)
		case "/session/login":
			loginCount++
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "test"})
			w.WriteHeader(http.StatusOK)
		case "/v1/consumers":
			if loginCount == 1 {
				w.WriteHeader(http.StatusUnauthorized)
			} else {
				w.WriteHeader(http.StatusOK)
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	c := NewHigressClient(Config{ConsoleURL: server.URL}, server.Client())

	// First call triggers 401 which clears cookies
	c.EnsureConsumer(context.Background(), ConsumerRequest{Name: "test", CredentialKey: "k"})
	// Second call should re-authenticate
	c.EnsureConsumer(context.Background(), ConsumerRequest{Name: "test", CredentialKey: "k"})

	if loginCount < 2 {
		t.Errorf("expected at least 2 logins after 401, got %d", loginCount)
	}
}
