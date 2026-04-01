package credentials

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestBuildWorkerPolicy(t *testing.T) {
	policy := BuildWorkerPolicy("my-bucket", "alice")

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(policy), &parsed); err != nil {
		t.Fatalf("policy is not valid JSON: %v", err)
	}

	stmts, ok := parsed["Statement"].([]interface{})
	if !ok || len(stmts) != 2 {
		t.Fatalf("expected 2 statements, got %v", parsed["Statement"])
	}

	// Check ListObjects statement has correct condition
	stmt0 := stmts[0].(map[string]interface{})
	cond := stmt0["Condition"].(map[string]interface{})
	sl := cond["StringLike"].(map[string]interface{})
	prefixes := sl["oss:Prefix"].([]interface{})
	if prefixes[0] != "agents/alice/*" {
		t.Errorf("expected agents/alice/*, got %v", prefixes[0])
	}
	if prefixes[1] != "shared/*" {
		t.Errorf("expected shared/*, got %v", prefixes[1])
	}

	// Check read/write statement has correct resources
	stmt1 := stmts[1].(map[string]interface{})
	resources := stmt1["Resource"].([]interface{})
	if resources[0] != "acs:oss:*:*:my-bucket/agents/alice/*" {
		t.Errorf("unexpected resource: %v", resources[0])
	}
	if resources[1] != "acs:oss:*:*:my-bucket/shared/*" {
		t.Errorf("unexpected resource: %v", resources[1])
	}
}

func TestIssueWorkerToken(t *testing.T) {
	// Mock STS endpoint
	mockSTS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		r.ParseForm()
		if r.FormValue("Action") != "AssumeRoleWithOIDC" {
			t.Errorf("expected AssumeRoleWithOIDC action")
		}
		if r.FormValue("DurationSeconds") != "3600" {
			t.Errorf("expected 3600 duration")
		}
		// Verify policy contains worker name
		policy := r.FormValue("Policy")
		if policy == "" {
			t.Error("expected non-empty policy")
		}
		var parsed map[string]interface{}
		json.Unmarshal([]byte(policy), &parsed)

		json.NewEncoder(w).Encode(map[string]interface{}{
			"Credentials": map[string]string{
				"AccessKeyId":     "test-ak",
				"AccessKeySecret": "test-sk",
				"SecurityToken":   "test-token",
				"Expiration":      "2026-03-26T12:00:00Z",
			},
		})
	}))
	defer mockSTS.Close()

	// Write temp OIDC token file
	tmpFile, _ := os.CreateTemp("", "oidc-token-*")
	tmpFile.WriteString("mock-oidc-token")
	tmpFile.Close()
	defer os.Remove(tmpFile.Name())

	svc := NewSTSServiceWithClient(STSConfig{
		Region:          "cn-hangzhou",
		RoleArn:         "acs:ram::123:role/test",
		OIDCProviderArn: "acs:ram::123:oidc-provider/test",
		OIDCTokenFile:   tmpFile.Name(),
		OSSBucket:       "test-bucket",
	}, mockSTS.Client())

	// Override endpoint to use mock server
	svc.endpointOverride = mockSTS.URL

	token, err := svc.IssueWorkerToken(t.Context(), "alice")
	if err != nil {
		t.Fatalf("IssueWorkerToken failed: %v", err)
	}
	if token.AccessKeyID != "test-ak" {
		t.Errorf("expected test-ak, got %s", token.AccessKeyID)
	}
	if token.OSSBucket != "test-bucket" {
		t.Errorf("expected test-bucket, got %s", token.OSSBucket)
	}
	if token.ExpiresInSec != 3600 {
		t.Errorf("expected 3600, got %d", token.ExpiresInSec)
	}
}

func TestIssueWorkerTokenSTSError(t *testing.T) {
	mockSTS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"Code":"NoPermission","Message":"forbidden"}`))
	}))
	defer mockSTS.Close()

	tmpFile, _ := os.CreateTemp("", "oidc-token-*")
	tmpFile.WriteString("mock-oidc-token")
	tmpFile.Close()
	defer os.Remove(tmpFile.Name())

	svc := NewSTSServiceWithClient(STSConfig{
		Region:          "cn-hangzhou",
		RoleArn:         "acs:ram::123:role/test",
		OIDCProviderArn: "acs:ram::123:oidc-provider/test",
		OIDCTokenFile:   tmpFile.Name(),
		OSSBucket:       "test-bucket",
	}, mockSTS.Client())
	svc.endpointOverride = mockSTS.URL

	_, err := svc.IssueWorkerToken(t.Context(), "alice")
	if err == nil {
		t.Error("expected error for STS 403")
	}
}
