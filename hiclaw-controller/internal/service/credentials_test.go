package service

import (
	"context"
	"testing"

	v1beta1 "github.com/hiclaw/hiclaw-controller/api/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	fakeclient "k8s.io/client-go/kubernetes/fake"
)

// TestSecretCredentialStore_StampsControllerLabel verifies the controller
// name provided at construction is copied onto every credential Secret
// under the hiclaw.io/controller key so that multi-instance deployments in
// one namespace can filter their own credential artifacts.
func TestSecretCredentialStore_StampsControllerLabel(t *testing.T) {
	client := fakeclient.NewSimpleClientset()
	store := &SecretCredentialStore{
		Client:         client,
		Namespace:      "hiclaw",
		ControllerName: "ctl-a",
	}

	creds := &WorkerCredentials{
		MatrixPassword: "pw",
		MinIOPassword:  "miniopw",
		GatewayKey:     "gw",
	}
	if err := store.Save(context.Background(), "alice", creds); err != nil {
		t.Fatalf("Save: %v", err)
	}

	sec, err := client.CoreV1().Secrets("hiclaw").Get(context.Background(), "hiclaw-creds-alice", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get secret: %v", err)
	}
	if got := sec.Labels[v1beta1.LabelController]; got != "ctl-a" {
		t.Fatalf("expected controller label ctl-a, got %q (labels=%v)", got, sec.Labels)
	}
	if sec.Labels["hiclaw.io/worker"] != "alice" {
		t.Fatalf("expected worker label alice, got %q", sec.Labels["hiclaw.io/worker"])
	}
}
