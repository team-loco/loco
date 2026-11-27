package controller

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-logr/logr"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	v1Gateway "sigs.k8s.io/gateway-api/apis/v1"

	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
	// +kubebuilder:scaffold:imports
)

var (
	ctx       context.Context
	cancel    context.CancelFunc
	testEnv   *envtest.Environment
	cfg       *rest.Config
	k8sClient client.Client
)

func TestControllers(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecs(t, "Controller Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(
		logr.FromSlogHandler(slog.NewTextHandler(GinkgoWriter, &slog.HandlerOptions{Level: slog.LevelDebug})),
	)

	ctx, cancel = context.WithCancel(context.Background())

	Expect(locov1alpha1.AddToScheme(scheme.Scheme)).To(Succeed())
	Expect(v1Gateway.Install(scheme.Scheme)).To(Succeed())

	// +kubebuilder:scaffold:scheme

	By("bootstrapping test environment")
	command := exec.CommandContext(ctx, "go", "list", "-m", "-f", "{{.Dir}}", "sigs.k8s.io/gateway-api")
	module, err := command.Output()
	Expect(err).NotTo(HaveOccurred())
	routeCRD := filepath.Join(
		strings.TrimSpace(string(module)), "config", "crd", "standard", "gateway.networking.k8s.io_httproutes.yaml",
	)
	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd", "bases"), routeCRD},
		ErrorIfCRDPathMissing: true,
	}

	if dir := firstEnvTestBinaryDir(); dir != "" {
		testEnv.BinaryAssetsDirectory = dir
	}

	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())
})

var _ = AfterSuite(func() {
	By("tearing down the test environment")
	cancel()
	err := testEnv.Stop()
	Expect(err).NotTo(HaveOccurred())
})

// firstEnvTestBinaryDir finds the envtest assets `make setup-envtest` downloads, for runs
// without KUBEBUILDER_ASSETS.
func firstEnvTestBinaryDir() string {
	basePath := filepath.Join("..", "..", "bin", "k8s")
	entries, err := os.ReadDir(basePath)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if entry.IsDir() {
			return filepath.Join(basePath, entry.Name())
		}
	}
	return ""
}
