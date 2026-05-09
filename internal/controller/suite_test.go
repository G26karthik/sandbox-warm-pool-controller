package controller

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	sandboxv1alpha1 "github.com/G26karthik/sandbox-warm-pool-controller/api/v1alpha1"
)

var (
	testEnv    *envtest.Environment
	k8sClient  client.Client
	testCtx    context.Context
	testCancel context.CancelFunc
	testScheme *runtime.Scheme
	reconciler *SandboxWarmPoolReconciler
)

func TestMain(m *testing.M) {
	testCtx, testCancel = context.WithCancel(context.Background())
	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd")},
		ErrorIfCRDPathMissing: true,
	}

	cfg, err := testEnv.Start()
	if err != nil {
		panic(err)
	}

	testScheme = runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(testScheme))
	utilruntime.Must(corev1.AddToScheme(testScheme))
	utilruntime.Must(sandboxv1alpha1.AddToScheme(testScheme))

	k8sClient, err = client.New(cfg, client.Options{Scheme: testScheme})
	if err != nil {
		panic(err)
	}

	reconciler = &SandboxWarmPoolReconciler{Client: k8sClient, Scheme: testScheme}

	code := m.Run()

	testCancel()
	_ = testEnv.Stop()
	os.Exit(code)
}
