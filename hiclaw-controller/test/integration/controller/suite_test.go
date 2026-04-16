//go:build integration

package controller_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	v1beta1 "github.com/hiclaw/hiclaw-controller/api/v1beta1"
	"github.com/hiclaw/hiclaw-controller/internal/backend"
	"github.com/hiclaw/hiclaw-controller/internal/controller"
	"github.com/hiclaw/hiclaw-controller/test/testutil"
	"github.com/hiclaw/hiclaw-controller/test/testutil/mocks"
	"go.uber.org/zap/zapcore"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

const (
	timeout  = 30 * time.Second
	interval = 250 * time.Millisecond
)

var (
	testEnv   *envtest.Environment
	k8sClient client.Client
	ctx       context.Context
	cancel    context.CancelFunc

	mockProv    *mocks.MockProvisioner
	mockDeploy  *mocks.MockDeployer
	mockBackend *mocks.MockWorkerBackend
	mockEnv     *mocks.MockEnvBuilder
)

func TestMain(m *testing.M) {
	testEnv = testutil.NewTestEnv()
	scheme := testutil.Scheme()

	cfg, err := testEnv.Start()
	if err != nil {
		panic(fmt.Sprintf("failed to start envtest: %v", err))
	}

	ctx, cancel = context.WithCancel(context.Background())
	ctrl.SetLogger(zap.New(zap.UseDevMode(true), zap.Level(zapcore.InfoLevel)))

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress: "0", // disable metrics server in tests
		},
	})
	if err != nil {
		panic(fmt.Sprintf("failed to create manager: %v", err))
	}

	// Create a cacheless client so tests always read the latest state.
	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		panic(fmt.Sprintf("failed to create k8s client: %v", err))
	}

	// Wire up mocks
	mockProv = mocks.NewMockProvisioner()
	mockDeploy = mocks.NewMockDeployer()
	mockBackend = mocks.NewMockWorkerBackend()
	mockEnv = mocks.NewMockEnvBuilder()

	backendRegistry := backend.NewRegistry(
		[]backend.WorkerBackend{mockBackend},
		nil,
	)

	reconciler := &controller.WorkerReconciler{
		Client:      mgr.GetClient(),
		Provisioner: mockProv,
		Deployer:    mockDeploy,
		Backend:     backendRegistry,
		EnvBuilder:  mockEnv,
		Legacy:      nil,
	}
	if err := reconciler.SetupWithManager(mgr); err != nil {
		panic(fmt.Sprintf("failed to setup WorkerReconciler: %v", err))
	}

	go func() {
		if err := mgr.Start(ctx); err != nil {
			panic(fmt.Sprintf("failed to start manager: %v", err))
		}
	}()

	// Wait for manager cache to sync
	if !mgr.GetCache().WaitForCacheSync(ctx) {
		panic("cache sync failed")
	}

	code := m.Run()

	cancel()
	if err := testEnv.Stop(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to stop envtest: %v\n", err)
	}

	os.Exit(code)
}

// resetMocks resets all mock call records and Fn overrides between tests.
func resetMocks() {
	mockProv.Reset()
	mockDeploy.Reset()
	mockBackend.Reset()
	mockEnv.Reset()
}

// suppress unused import for v1beta1
var _ = v1beta1.GroupName
