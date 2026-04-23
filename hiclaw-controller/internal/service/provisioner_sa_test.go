package service

import (
	"context"
	"testing"

	v1beta1 "github.com/hiclaw/hiclaw-controller/api/v1beta1"
	authpkg "github.com/hiclaw/hiclaw-controller/internal/auth"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	fakeclient "k8s.io/client-go/kubernetes/fake"
)

// TestProvisioner_EnsureServiceAccount_StampsControllerLabel verifies that
// Worker and Manager SAs created by the Provisioner carry
// hiclaw.io/controller so peer instances do not treat them as their own.
func TestProvisioner_EnsureServiceAccount_StampsControllerLabel(t *testing.T) {
	client := fakeclient.NewSimpleClientset()
	p := NewProvisioner(ProvisionerConfig{
		K8sClient:      client,
		Namespace:      "hiclaw",
		ResourcePrefix: authpkg.ResourcePrefix("hiclaw-"),
		ControllerName: "ctl-b",
	})

	if err := p.EnsureServiceAccount(context.Background(), "alice"); err != nil {
		t.Fatalf("EnsureServiceAccount: %v", err)
	}
	sa, err := client.CoreV1().ServiceAccounts("hiclaw").Get(context.Background(), "hiclaw-worker-alice", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get SA: %v", err)
	}
	if got := sa.Labels[v1beta1.LabelController]; got != "ctl-b" {
		t.Fatalf("worker SA: expected controller label ctl-b, got %q (labels=%v)", got, sa.Labels)
	}

	if err := p.EnsureManagerServiceAccount(context.Background(), "default"); err != nil {
		t.Fatalf("EnsureManagerServiceAccount: %v", err)
	}
	mgrSA, err := client.CoreV1().ServiceAccounts("hiclaw").Get(context.Background(), "hiclaw-manager", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get manager SA: %v", err)
	}
	if got := mgrSA.Labels[v1beta1.LabelController]; got != "ctl-b" {
		t.Fatalf("manager SA: expected controller label ctl-b, got %q (labels=%v)", got, mgrSA.Labels)
	}
}
