package credentials

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/alibaba/hiclaw/orchestrator/auth"
)

func TestHandlerRefreshToken(t *testing.T) {
	// Mock STS endpoint
	mockSTS := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

	tmpFile, _ := os.CreateTemp("", "oidc-*")
	tmpFile.WriteString("mock-token")
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

	h := NewHandler(svc)

	// Build request with worker identity in context
	req := httptest.NewRequest(http.MethodPost, "/credentials/sts", nil)
	ctx := context.WithValue(req.Context(), auth.CallerKeyForTest(), &auth.CallerIdentity{
		Role:       "worker",
		WorkerName: "alice",
	})
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()
	h.RefreshToken(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var token STSToken
	json.NewDecoder(w.Body).Decode(&token)
	if token.AccessKeyID != "test-ak" {
		t.Errorf("expected test-ak, got %s", token.AccessKeyID)
	}
	if token.OSSBucket != "test-bucket" {
		t.Errorf("expected test-bucket, got %s", token.OSSBucket)
	}
}

func TestHandlerNoSTSService(t *testing.T) {
	h := NewHandler(nil)

	req := httptest.NewRequest(http.MethodPost, "/credentials/sts", nil)
	w := httptest.NewRecorder()
	h.RefreshToken(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", w.Code)
	}
}

func TestHandlerMissingWorkerIdentity(t *testing.T) {
	tmpFile, _ := os.CreateTemp("", "oidc-*")
	tmpFile.WriteString("mock-token")
	tmpFile.Close()
	defer os.Remove(tmpFile.Name())

	svc := NewSTSService(STSConfig{
		Region:        "cn-hangzhou",
		OIDCTokenFile: tmpFile.Name(),
	})
	h := NewHandler(svc)

	// Request without caller identity in context
	req := httptest.NewRequest(http.MethodPost, "/credentials/sts", nil)
	w := httptest.NewRecorder()
	h.RefreshToken(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}
