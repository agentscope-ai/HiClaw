package backend

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type fakeK8sCoreClient struct {
	pods map[string]map[string]*corev1.Pod
}

func newFakeK8sCoreClient(objects ...*corev1.Pod) *fakeK8sCoreClient {
	client := &fakeK8sCoreClient{pods: map[string]map[string]*corev1.Pod{}}
	for _, obj := range objects {
		ns := obj.Namespace
		if ns == "" {
			ns = "default"
		}
		if client.pods[ns] == nil {
			client.pods[ns] = map[string]*corev1.Pod{}
		}
		client.pods[ns][obj.Name] = obj.DeepCopy()
	}
	return client
}

func (f *fakeK8sCoreClient) Pods(namespace string) K8sPodClient {
	if f.pods[namespace] == nil {
		f.pods[namespace] = map[string]*corev1.Pod{}
	}
	return &fakeK8sPodClient{
		namespace: namespace,
		store:     f.pods[namespace],
	}
}

type fakeK8sPodClient struct {
	namespace string
	store     map[string]*corev1.Pod
}

func (f *fakeK8sPodClient) Get(_ context.Context, name string, _ metav1.GetOptions) (*corev1.Pod, error) {
	if pod, ok := f.store[name]; ok {
		return pod.DeepCopy(), nil
	}
	return nil, apierrors.NewNotFound(schema.GroupResource{Resource: "pods"}, name)
}

func (f *fakeK8sPodClient) Create(_ context.Context, pod *corev1.Pod, _ metav1.CreateOptions) (*corev1.Pod, error) {
	if _, exists := f.store[pod.Name]; exists {
		return nil, apierrors.NewAlreadyExists(schema.GroupResource{Resource: "pods"}, pod.Name)
	}
	created := pod.DeepCopy()
	if created.Namespace == "" {
		created.Namespace = f.namespace
	}
	f.store[created.Name] = created
	return created.DeepCopy(), nil
}

func (f *fakeK8sPodClient) Delete(_ context.Context, name string, _ metav1.DeleteOptions) error {
	if _, exists := f.store[name]; !exists {
		return apierrors.NewNotFound(schema.GroupResource{Resource: "pods"}, name)
	}
	delete(f.store, name)
	return nil
}

func (f *fakeK8sPodClient) List(_ context.Context, opts metav1.ListOptions) (*corev1.PodList, error) {
	list := &corev1.PodList{}
	for _, pod := range f.store {
		if opts.LabelSelector != "" && strings.Contains(opts.LabelSelector, "app=hiclaw-worker") && pod.Labels["app"] != "hiclaw-worker" {
			continue
		}
		list.Items = append(list.Items, *pod.DeepCopy())
	}
	return list, nil
}

func newTestK8sBackend(objects ...*corev1.Pod) *K8sBackend {
	client := newFakeK8sCoreClient(objects...)
	return NewK8sBackendWithClient(client, K8sConfig{
		Namespace:        "hiclaw",
		WorkerImage:      "hiclaw/worker-agent:latest",
		CopawWorkerImage: "hiclaw/copaw-worker:latest",
		WorkerCPU:        "1000m",
		WorkerMemory:     "2Gi",
	}, "hiclaw-worker-")
}

func TestK8sCreate(t *testing.T) {
	t.Setenv("HICLAW_FS_BUCKET", "hiclaw-fs")
	t.Setenv("HICLAW_REGION", "cn-hangzhou")

	b := newTestK8sBackend()

	result, err := b.Create(context.Background(), CreateRequest{
		Name: "alice",
		Env: map[string]string{
			"HICLAW_MATRIX_URL": "http://matrix:6167",
		},
		ControllerURL:      "http://controller:8090",
		ServiceAccountName: "hiclaw-worker-test1",
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if result.Backend != "k8s" {
		t.Fatalf("expected k8s backend, got %s", result.Backend)
	}
	if result.Status != StatusStarting {
		t.Fatalf("expected starting status, got %s", result.Status)
	}

	pod, err := b.client.Pods("hiclaw").Get(context.Background(), "hiclaw-worker-alice", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("expected worker pod to exist: %v", err)
	}
	if pod.Spec.ServiceAccountName != "hiclaw-worker-test1" {
		t.Fatalf("expected SA hiclaw-worker-test1, got %q", pod.Spec.ServiceAccountName)
	}
	if pod.Spec.AutomountServiceAccountToken == nil || *pod.Spec.AutomountServiceAccountToken {
		t.Fatalf("expected default automount disabled")
	}
	if len(pod.Spec.Volumes) != 1 || pod.Spec.Volumes[0].Name != "hiclaw-token" {
		t.Fatalf("expected projected volume hiclaw-token, got %+v", pod.Spec.Volumes)
	}
	projSrc := pod.Spec.Volumes[0].Projected.Sources[0].ServiceAccountToken
	if projSrc.Audience != "hiclaw-controller" {
		t.Fatalf("expected default audience hiclaw-controller, got %q", projSrc.Audience)
	}

	envs := map[string]string{}
	for _, env := range pod.Spec.Containers[0].Env {
		envs[env.Name] = env.Value
	}
	if envs["HICLAW_RUNTIME"] != "k8s" {
		t.Fatalf("expected HICLAW_RUNTIME=k8s, got %q", envs["HICLAW_RUNTIME"])
	}
	if envs["HICLAW_AUTH_TOKEN_FILE"] != "/var/run/secrets/hiclaw/token" {
		t.Fatalf("expected HICLAW_AUTH_TOKEN_FILE, got %q", envs["HICLAW_AUTH_TOKEN_FILE"])
	}
	if envs["HICLAW_CONTROLLER_URL"] != "http://controller:8090" {
		t.Fatalf("expected injected controller URL, got %q", envs["HICLAW_CONTROLLER_URL"])
	}
	if envs["HICLAW_FS_BUCKET"] != "hiclaw-fs" {
		t.Fatalf("expected HICLAW_FS_BUCKET from process env, got %q", envs["HICLAW_FS_BUCKET"])
	}
	if _, ok := envs["HICLAW_OSS_BUCKET"]; ok {
		t.Fatalf("unexpected legacy HICLAW_OSS_BUCKET in worker pod env")
	}
	if envs["HICLAW_REGION"] != "cn-hangzhou" {
		t.Fatalf("expected HICLAW_REGION from process env, got %q", envs["HICLAW_REGION"])
	}
}

func TestK8sCreateCustomAudience(t *testing.T) {
	b := newTestK8sBackend()

	_, err := b.Create(context.Background(), CreateRequest{
		Name:         "bob",
		AuthAudience: "custom-audience",
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	pod, err := b.client.Pods("hiclaw").Get(context.Background(), "hiclaw-worker-bob", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("expected worker pod to exist: %v", err)
	}
	projSrc := pod.Spec.Volumes[0].Projected.Sources[0].ServiceAccountToken
	if projSrc.Audience != "custom-audience" {
		t.Fatalf("expected custom-audience, got %q", projSrc.Audience)
	}
}

func TestK8sCreateConflict(t *testing.T) {
	existingPod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hiclaw-worker-alice",
			Namespace: "hiclaw",
		},
	}
	b := newTestK8sBackend(existingPod)

	_, err := b.Create(context.Background(), CreateRequest{Name: "alice"})
	if err == nil {
		t.Fatal("expected conflict error")
	}
}

func TestK8sStatus(t *testing.T) {
	b := newTestK8sBackend(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hiclaw-worker-bob",
			Namespace: "hiclaw",
			Labels: map[string]string{
				"app":              "hiclaw-worker",
				"hiclaw.io/worker": "bob",
			},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	})

	result, err := b.Status(context.Background(), "bob")
	if err != nil {
		t.Fatalf("Status failed: %v", err)
	}
	if result.Status != StatusRunning {
		t.Fatalf("expected running, got %s", result.Status)
	}
}

func TestK8sStopAndDelete(t *testing.T) {
	b := newTestK8sBackend(&corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "hiclaw-worker-carol",
			Namespace: "hiclaw",
		},
	})

	if err := b.Stop(context.Background(), "carol"); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}

	result, err := b.Status(context.Background(), "carol")
	if err != nil {
		t.Fatalf("Status failed: %v", err)
	}
	if result.Status != StatusNotFound {
		t.Fatalf("expected not_found after stop, got %s", result.Status)
	}
}

func TestK8sList(t *testing.T) {
	b := newTestK8sBackend(
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "hiclaw-worker-w1",
				Namespace: "hiclaw",
				Labels: map[string]string{
					"app":               "hiclaw-worker",
					"hiclaw.io/worker":  "w1",
					"hiclaw.io/runtime": "openclaw",
				},
			},
			Status: corev1.PodStatus{Phase: corev1.PodRunning},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "hiclaw-worker-w2",
				Namespace: "hiclaw",
				Labels: map[string]string{
					"app":               "hiclaw-worker",
					"hiclaw.io/worker":  "w2",
					"hiclaw.io/runtime": "copaw",
				},
			},
			Status: corev1.PodStatus{Phase: corev1.PodPending},
		},
	)

	workers, err := b.List(context.Background())
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(workers) != 2 {
		t.Fatalf("expected 2 workers, got %d", len(workers))
	}
}

func TestNormalizeK8sPodPhase(t *testing.T) {
	cases := []struct {
		phase    corev1.PodPhase
		expected WorkerStatus
	}{
		{corev1.PodRunning, StatusRunning},
		{corev1.PodPending, StatusStarting},
		{corev1.PodSucceeded, StatusStopped},
		{corev1.PodFailed, StatusStopped},
		{corev1.PodUnknown, StatusUnknown},
	}
	for _, tc := range cases {
		if got := normalizeK8sPodPhase(tc.phase); got != tc.expected {
			t.Fatalf("normalizeK8sPodPhase(%q)=%s, want %s", tc.phase, got, tc.expected)
		}
	}
}

func TestBuildHostAliases(t *testing.T) {
	aliases := buildHostAliases([]string{
		"matrix-local.hiclaw.io:10.0.0.1",
		"aigw-local.hiclaw.io:10.0.0.1",
		"bad-entry",
	})
	if len(aliases) != 1 {
		t.Fatalf("expected 1 host alias, got %d", len(aliases))
	}
	if len(aliases[0].Hostnames) != 2 {
		t.Fatalf("expected 2 hostnames, got %d", len(aliases[0].Hostnames))
	}
}
