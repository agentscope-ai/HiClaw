package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGenerateWorkerKey(t *testing.T) {
	ks := NewKeyStore("manager-secret", nil)

	k1 := ks.GenerateWorkerKey("alice")
	k2 := ks.GenerateWorkerKey("bob")

	if k1 == k2 {
		t.Error("expected unique keys")
	}
	if len(k1) != 64 { // 32 bytes hex
		t.Errorf("expected 64 char hex key, got %d", len(k1))
	}
}

func TestGenerateWorkerKeyOverwrite(t *testing.T) {
	ks := NewKeyStore("mgr", nil)

	old := ks.GenerateWorkerKey("alice")
	new := ks.GenerateWorkerKey("alice")

	if old == new {
		t.Error("regenerated key should differ")
	}

	// Old key should no longer validate
	if _, ok := ks.ValidateKey(old); ok {
		t.Error("old key should be invalid after regeneration")
	}
	id, ok := ks.ValidateKey(new)
	if !ok || id.WorkerName != "alice" {
		t.Error("new key should validate as alice")
	}
}

func TestValidateManagerKey(t *testing.T) {
	ks := NewKeyStore("mgr-key", nil)

	id, ok := ks.ValidateKey("mgr-key")
	if !ok || id.Role != "manager" {
		t.Error("expected manager identity")
	}
}

func TestValidateWorkerKey(t *testing.T) {
	ks := NewKeyStore("mgr", nil)
	key := ks.GenerateWorkerKey("bob")

	id, ok := ks.ValidateKey(key)
	if !ok || id.Role != "worker" || id.WorkerName != "bob" {
		t.Errorf("expected worker bob, got %+v", id)
	}
}

func TestValidateInvalidKey(t *testing.T) {
	ks := NewKeyStore("mgr", nil)

	if _, ok := ks.ValidateKey("bad-key"); ok {
		t.Error("expected invalid key to fail")
	}
	if _, ok := ks.ValidateKey(""); ok {
		t.Error("expected empty key to fail")
	}
}

func TestRemoveWorkerKey(t *testing.T) {
	ks := NewKeyStore("mgr", nil)
	key := ks.GenerateWorkerKey("alice")

	ks.RemoveWorkerKey("alice")

	if _, ok := ks.ValidateKey(key); ok {
		t.Error("removed key should be invalid")
	}
}

func TestSetWorkerKey(t *testing.T) {
	ks := NewKeyStore("mgr", nil)
	ks.SetWorkerKey("carol", "known-key-123")

	id, ok := ks.ValidateKey("known-key-123")
	if !ok || id.WorkerName != "carol" {
		t.Error("expected SetWorkerKey to work")
	}
}

func TestAuthDisabled(t *testing.T) {
	ks := NewKeyStore("", nil) // empty = auth disabled
	if ks.AuthEnabled() {
		t.Error("expected auth disabled with empty manager key")
	}
}

func TestMiddlewareSkipsWhenDisabled(t *testing.T) {
	ks := NewKeyStore("", nil)
	mw := NewMiddleware(ks)

	called := false
	handler := mw.RequireManager(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/workers", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if !called {
		t.Error("handler should be called when auth disabled")
	}
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestMiddlewareRequireManagerValid(t *testing.T) {
	ks := NewKeyStore("mgr-secret", nil)
	mw := NewMiddleware(ks)

	called := false
	handler := mw.RequireManager(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))

	req := httptest.NewRequest(http.MethodGet, "/workers", nil)
	req.Header.Set("Authorization", "Bearer mgr-secret")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if !called {
		t.Error("handler should be called for valid manager key")
	}
}

func TestMiddlewareRequireManagerRejectsWorker(t *testing.T) {
	ks := NewKeyStore("mgr-secret", nil)
	mw := NewMiddleware(ks)
	workerKey := ks.GenerateWorkerKey("alice")

	handler := mw.RequireManager(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called")
	}))

	req := httptest.NewRequest(http.MethodGet, "/workers", nil)
	req.Header.Set("Authorization", "Bearer "+workerKey)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", w.Code)
	}
}

func TestMiddlewareRequireManagerRejectsNoAuth(t *testing.T) {
	ks := NewKeyStore("mgr-secret", nil)
	mw := NewMiddleware(ks)

	handler := mw.RequireManager(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called")
	}))

	req := httptest.NewRequest(http.MethodGet, "/workers", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Code)
	}
}

func TestMiddlewareRequireWorkerValid(t *testing.T) {
	ks := NewKeyStore("mgr", nil)
	mw := NewMiddleware(ks)
	key := ks.GenerateWorkerKey("bob")

	var gotIdentity *CallerIdentity
	handler := mw.RequireWorker(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotIdentity = CallerFromContext(r.Context())
	}))

	req := httptest.NewRequest(http.MethodPost, "/credentials/sts", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if gotIdentity == nil || gotIdentity.WorkerName != "bob" {
		t.Errorf("expected worker bob in context, got %+v", gotIdentity)
	}
}

func TestMiddlewareRequireWorkerRejectsManager(t *testing.T) {
	ks := NewKeyStore("mgr-secret", nil)
	mw := NewMiddleware(ks)

	handler := mw.RequireWorker(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler should not be called")
	}))

	req := httptest.NewRequest(http.MethodPost, "/credentials/sts", nil)
	req.Header.Set("Authorization", "Bearer mgr-secret")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", w.Code)
	}
}
