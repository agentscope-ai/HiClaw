package accessresolver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	v1beta1 "github.com/hiclaw/hiclaw-controller/api/v1beta1"
	"github.com/hiclaw/hiclaw-controller/internal/auth"
	"github.com/hiclaw/hiclaw-controller/internal/credprovider"
)

const testNS = "hiclaw"

func newFakeClient(t *testing.T, objs ...client.Object) client.Client {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := v1beta1.AddToScheme(scheme); err != nil {
		t.Fatalf("register scheme: %v", err)
	}
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
}

func rawJSON(t *testing.T, v any) *apiextensionsv1.JSON {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return &apiextensionsv1.JSON{Raw: b}
}

func TestResolveWorker_DefaultEntries(t *testing.T) {
	worker := &v1beta1.Worker{}
	worker.Name = "alice"
	worker.Namespace = testNS
	c := newFakeClient(t, worker)

	r := New(c, testNS, "hiclaw-test", "")
	session, entries, err := r.ResolveForCaller(context.Background(), &auth.CallerIdentity{
		Role: auth.RoleWorker, Username: "alice", WorkerName: "alice",
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if session != "hiclaw-worker-alice" {
		t.Fatalf("session = %q", session)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 default entries, got %d", len(entries))
	}
	for _, e := range entries {
		if e.Scope.Bucket != "hiclaw-test" {
			t.Fatalf("bucket not resolved: %+v", e.Scope)
		}
	}
	if got := entries[0].Scope.Prefixes[0]; got != "agents/alice/*" {
		t.Fatalf("template not expanded: %q", got)
	}
}

func TestResolveWorker_CustomBucketRef(t *testing.T) {
	worker := &v1beta1.Worker{}
	worker.Name = "bob"
	worker.Namespace = testNS
	worker.Spec.AccessEntries = []v1beta1.AccessEntry{
		{
			Service:     credprovider.ServiceObjectStorage,
			Permissions: []string{"read"},
			Scope: rawJSON(t, map[string]any{
				"bucketRef": "workspace",
				"prefixes":  []string{"custom/${self.name}/*"},
			}),
		},
	}
	c := newFakeClient(t, worker)

	r := New(c, testNS, "hiclaw-test", "")
	_, entries, err := r.ResolveForCaller(context.Background(), &auth.CallerIdentity{
		Role: auth.RoleWorker, Username: "bob", WorkerName: "bob",
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries", len(entries))
	}
	got := entries[0]
	if got.Scope.Bucket != "hiclaw-test" {
		t.Fatalf("bucket = %q", got.Scope.Bucket)
	}
	if got.Scope.Prefixes[0] != "custom/bob/*" {
		t.Fatalf("prefix = %q", got.Scope.Prefixes[0])
	}
}

func TestResolveWorker_UnknownService(t *testing.T) {
	worker := &v1beta1.Worker{}
	worker.Name = "eve"
	worker.Namespace = testNS
	worker.Spec.AccessEntries = []v1beta1.AccessEntry{
		{Service: "nonsense", Scope: rawJSON(t, map[string]any{})},
	}
	c := newFakeClient(t, worker)

	r := New(c, testNS, "hiclaw-test", "")
	_, _, err := r.ResolveForCaller(context.Background(), &auth.CallerIdentity{
		Role: auth.RoleWorker, Username: "eve", WorkerName: "eve",
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported service") {
		t.Fatalf("expected unsupported-service error, got: %v", err)
	}
}

func TestResolveWorker_ObjectStorageMissingPrefixes(t *testing.T) {
	worker := &v1beta1.Worker{}
	worker.Name = "dave"
	worker.Namespace = testNS
	worker.Spec.AccessEntries = []v1beta1.AccessEntry{
		{
			Service: credprovider.ServiceObjectStorage,
			Scope:   rawJSON(t, map[string]any{"bucket": "other"}),
		},
	}
	c := newFakeClient(t, worker)

	r := New(c, testNS, "hiclaw-test", "")
	_, _, err := r.ResolveForCaller(context.Background(), &auth.CallerIdentity{
		Role: auth.RoleWorker, Username: "dave", WorkerName: "dave",
	})
	if err == nil || !strings.Contains(err.Error(), "prefixes is empty") {
		t.Fatalf("expected prefixes-empty error, got: %v", err)
	}
}

func TestResolveManager_Defaults(t *testing.T) {
	mgr := &v1beta1.Manager{}
	mgr.Name = "manager"
	mgr.Namespace = testNS
	c := newFakeClient(t, mgr)

	r := New(c, testNS, "hiclaw-test", "gw-1")
	session, entries, err := r.ResolveForCaller(context.Background(), &auth.CallerIdentity{
		Role: auth.RoleManager, Username: "manager",
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if session != "hiclaw-manager-manager" {
		t.Fatalf("session = %q", session)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 default entry, got %d", len(entries))
	}
	prefixes := entries[0].Scope.Prefixes
	wantManager := false
	for _, p := range prefixes {
		if p == "manager/*" {
			wantManager = true
		}
	}
	if !wantManager {
		t.Fatalf("manager default entries missing 'manager/*': %+v", prefixes)
	}
}

func TestResolve_GatewayAdminHappyPath(t *testing.T) {
	worker := &v1beta1.Worker{}
	worker.Name = "gw-bot"
	worker.Namespace = testNS
	worker.Spec.AccessEntries = []v1beta1.AccessEntry{
		{
			Service:     credprovider.ServiceGatewayAdmin,
			Permissions: []string{"read", "write"},
			Scope: rawJSON(t, map[string]any{
				"gatewayRef": "default",
				"resources":  []string{"consumers/*", "routes/*"},
			}),
		},
	}
	c := newFakeClient(t, worker)

	r := New(c, testNS, "hiclaw-test", "gw-abc123")
	_, entries, err := r.ResolveForCaller(context.Background(), &auth.CallerIdentity{
		Role: auth.RoleWorker, Username: "gw-bot", WorkerName: "gw-bot",
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("got %d entries", len(entries))
	}
	got := entries[0]
	if got.Service != credprovider.ServiceGatewayAdmin {
		t.Fatalf("service = %q", got.Service)
	}
	if got.Scope.GatewayID != "gw-abc123" {
		t.Fatalf("gatewayId = %q", got.Scope.GatewayID)
	}
	if len(got.Scope.Resources) != 2 {
		t.Fatalf("resources = %+v", got.Scope.Resources)
	}
}

func TestResolve_GatewayAdminNoDefault(t *testing.T) {
	worker := &v1beta1.Worker{}
	worker.Name = "gw-bot2"
	worker.Namespace = testNS
	worker.Spec.AccessEntries = []v1beta1.AccessEntry{
		{
			Service: credprovider.ServiceGatewayAdmin,
			Scope:   rawJSON(t, map[string]any{"gatewayRef": "default"}),
		},
	}
	c := newFakeClient(t, worker)

	r := New(c, testNS, "hiclaw-test", "")
	_, _, err := r.ResolveForCaller(context.Background(), &auth.CallerIdentity{
		Role: auth.RoleWorker, Username: "gw-bot2", WorkerName: "gw-bot2",
	})
	if err == nil || !strings.Contains(err.Error(), "no AI Gateway configured") {
		t.Fatalf("expected no-AI-Gateway error, got: %v", err)
	}
}

func TestControllerDefaults(t *testing.T) {
	entries := ControllerDefaults("b1", "")
	if len(entries) != 1 || entries[0].Service != credprovider.ServiceObjectStorage {
		t.Fatalf("expected single object-storage entry, got %+v", entries)
	}

	entries = ControllerDefaults("b1", "gw-1")
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries with gateway, got %d", len(entries))
	}
	if entries[1].Service != credprovider.ServiceGatewayAdmin || entries[1].Scope.GatewayID != "gw-1" {
		t.Fatalf("unexpected second entry: %+v", entries[1])
	}
}

func TestResolveForCaller_RejectedRoles(t *testing.T) {
	r := New(newFakeClient(t), testNS, "b", "")
	_, _, err := r.ResolveForCaller(context.Background(), &auth.CallerIdentity{Role: auth.RoleAdmin})
	if err == nil {
		t.Fatalf("expected error for admin role")
	}
}
