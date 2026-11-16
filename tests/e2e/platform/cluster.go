package platform

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"testing"
)

// Cluster is the kind cluster of a run and the commands its tests run against it. A shard that
// installs charts on a bare cluster starts one from its TestMain with NewCluster and Run; a
// Platform runs the whole install on one.
type Cluster struct {
	// Version is the version every binary and image of the run reports.
	Version string

	shard         string
	root          string
	run           string
	dir           string
	kubeconfig    string
	kubeContext   string
	cluster       string
	env           []string
	keep          bool
	skipBuild     bool
	clusterReused bool
}

// NewCluster reads the run's configuration for a shard. It starts nothing.
func NewCluster(shard string) *Cluster {
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
	runID := os.Getenv("E2E_RUN_ID")
	if runID == "" {
		runID = fmt.Sprintf("%08x", hash(root))
	}
	if !runIDPattern.MatchString(runID) {
		panic(fmt.Sprintf("E2E_RUN_ID must be 1-16 lowercase letters, digits or inner hyphens, got %q", runID))
	}
	run := runID + "-" + shard
	dir := filepath.Join(root, "tests", "e2e", "runs", run)
	c := &Cluster{
		Version:    envOr("E2E_VERSION", defaultVersion),
		shard:      shard,
		root:       root,
		run:        run,
		dir:        dir,
		kubeconfig: filepath.Join(dir, "kubeconfig"),
		cluster:    "loco-e2e-" + run,
		keep:       envBool("E2E_KEEP"),
		skipBuild:  envBool("E2E_SKIP_BUILD"),
	}
	c.kubeContext = "kind-" + c.cluster
	c.env = append(os.Environ(), "KUBECONFIG="+c.kubeconfig)
	return c
}

// Run creates or reuses the kind cluster, runs setup and the package's tests, and deletes the
// cluster, or leaves it running with E2E_KEEP. It returns the exit code.
func (c *Cluster) Run(m *testing.M, setup func(ctx context.Context) error) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if envBool("E2E_TEARDOWN_ONLY") {
		return c.teardown(ctx, 0)
	}
	code := c.runTests(ctx, m, setup)
	if c.keep {
		logf("Leaving cluster %s running; rerun it with mise run e2e --keep --skip-build %[2]s "+
			"and remove it with mise run e2e:teardown %[2]s", c.cluster, c.shard)
		return code
	}
	return c.teardown(context.WithoutCancel(ctx), code)
}

func (c *Cluster) runTests(ctx context.Context, m *testing.M, setup func(ctx context.Context) error) int {
	setupCtx, cancel := context.WithTimeout(ctx, setupTimeout)
	defer cancel()
	if err := mkdirReadable(c.dir); err != nil {
		logf("setup of run %s failed: %v", c.run, err)
		return 1
	}
	if err := c.setupKind(setupCtx); err != nil {
		logf("setup of run %s failed: %v", c.run, err)
		c.dumpCluster(ctx)
		return 1
	}
	if err := setup(setupCtx); err != nil {
		logf("setup of run %s failed: %v", c.run, err)
		c.dumpCluster(ctx)
		return 1
	}
	logf("Cluster %s ready", c.cluster)
	return m.Run()
}

func (c *Cluster) teardown(ctx context.Context, code int) int {
	err := c.deleteCluster(ctx)
	if err == nil {
		err = os.RemoveAll(c.dir)
	}
	if err != nil {
		logf("teardown: %v", err)
		return 1
	}
	return code
}

func (c *Cluster) deleteCluster(ctx context.Context) error {
	logf("Tearing down run %s", c.run)
	clusters, err := c.Command(ctx, "kind", "get", "clusters")
	if err != nil {
		return err
	}
	if !slices.Contains(strings.Fields(clusters), c.cluster) {
		return nil
	}
	_, err = c.Command(ctx, "kind", "delete", "cluster", "--name", c.cluster, "--kubeconfig", c.kubeconfig)
	return err
}

// KindCluster is the name of the run's kind cluster.
func (c *Cluster) KindCluster() string {
	return c.cluster
}

// Path is a path of the repository.
func (c *Cluster) Path(elem ...string) string {
	return filepath.Join(append([]string{c.root}, elem...)...)
}

// Dir is the run's directory, which teardown removes.
func (c *Cluster) Dir() string {
	return c.dir
}

// SkipBuild reports whether the run reuses the images of the last one.
func (c *Cluster) SkipBuild() bool {
	return c.skipBuild
}

// Mise runs a mise task of the repository.
func (c *Cluster) Mise(ctx context.Context, env []string, args ...string) (string, error) {
	return c.mise(ctx, env, args...)
}

// Kind runs a kind command against the run's cluster, such as load docker-image.
func (c *Cluster) Kind(ctx context.Context, args ...string) (string, error) {
	return c.Command(ctx, "kind", append(args, "--name", c.cluster)...)
}

// Helm runs a helm command against the run's cluster.
func (c *Cluster) Helm(ctx context.Context, args ...string) (string, error) {
	return c.Command(ctx, "helm", append(args, "--kubeconfig", c.kubeconfig, "--kube-context", c.kubeContext)...)
}
