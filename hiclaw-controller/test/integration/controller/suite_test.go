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
	"k8s.io/client-go/rest"
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
	restCfg   *rest.Config // shared with leaderelection_test.go
	k8sClient client.Client
	ctx       context.Context
	cancel    context.CancelFunc

	// Worker mocks
	mockProv    *mocks.MockProvisioner
	mockDeploy  *mocks.MockDeployer
	mockBackend *mocks.MockWorkerBackend
	mockEnv     *mocks.MockEnvBuilder

	// Manager mocks
	mockMgrProv    *mocks.MockManagerProvisioner
	mockMgrDeploy  *mocks.MockManagerDeployer
	mockMgrBackend *mocks.MockWorkerBackend
	mockMgrEnv     *mocks.MockManagerEnvBuilder
)

func TestMain(m *testing.M) {
	testEnv = testutil.NewTestEnv()
	scheme := testutil.Scheme()

	var err error
	restCfg, err = testEnv.Start()
	if err != nil {
		panic(fmt.Sprintf("failed to start envtest: %v", err))
	}

	ctx, cancel = context.WithCancel(context.Background())
	ctrl.SetLogger(zap.New(zap.UseDevMode(true), zap.Level(zapcore.InfoLevel)))

	mgr, err := ctrl.NewManager(restCfg, ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress: "0", // disable metrics server in tests
		},
	})
	if err != nil {
		panic(fmt.Sprintf("failed to create manager: %v", err))
	}

	// Create a cacheless client so tests always read the latest state.
	k8sClient, err = client.New(restCfg, client.Options{Scheme: scheme})
	if err != nil {
		panic(fmt.Sprintf("failed to create k8s client: %v", err))
	}

	// Wire up Worker mocks
	mockProv = mocks.NewMockProvisioner()
	mockDeploy = mocks.NewMockDeployer()
	mockBackend = mocks.NewMockWorkerBackend()
	mockEnv = mocks.NewMockEnvBuilder()

	workerBackendRegistry := backend.NewRegistry(
		[]backend.WorkerBackend{mockBackend},
		nil,
	)

	workerReconciler := &controller.WorkerReconciler{
		Client:      mgr.GetClient(),
		Provisioner: mockProv,
		Deployer:    mockDeploy,
		Backend:     workerBackendRegistry,
		EnvBuilder:  mockEnv,
		Legacy:      nil,
	}
	if err := workerReconciler.SetupWithManager(mgr); err != nil {
		panic(fmt.Sprintf("failed to setup WorkerReconciler: %v", err))
	}

	// Wire up Manager mocks
	mockMgrProv = mocks.NewMockManagerProvisioner()
	mockMgrDeploy = mocks.NewMockManagerDeployer()
	mockMgrBackend = mocks.NewMockWorkerBackend()
	mockMgrEnv = mocks.NewMockManagerEnvBuilder()

	mgrBackendRegistry := backend.NewRegistry(
		[]backend.WorkerBackend{mockMgrBackend},
		nil,
	)

	managerReconciler := &controller.ManagerReconciler{
		Client:      mgr.GetClient(),
		Provisioner: mockMgrProv,
		Deployer:    mockMgrDeploy,
		Backend:     mgrBackendRegistry,
		EnvBuilder:  mockMgrEnv,
	}
	if err := managerReconciler.SetupWithManager(mgr); err != nil {
		panic(fmt.Sprintf("failed to setup ManagerReconciler: %v", err))
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

// resetManagerMocks resets all Manager mock call records and Fn overrides.
func resetManagerMocks() {
	mockMgrProv.Reset()
	mockMgrDeploy.Reset()
	mockMgrBackend.Reset()
	mockMgrEnv.Reset()
}

// suppress unused import for v1beta1
var _ = v1beta1.GroupName
