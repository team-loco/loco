// Package platform brings up the Loco install an end-to-end test package runs against: a kind
// cluster with the loco-worker chart, the compose services, and the API, agent and observability
// proxy as host processes. Each shard package starts one from its TestMain, and its tests share it,
// each in its own organization and workspace.
package platform

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	corev1 "k8s.io/api/core/v1"

	"github.com/team-loco/loco/internal/envconfig"
	"github.com/team-loco/loco/internal/rpc"
)

// Features selects the install of a shard.
type Features struct {
	// Name names the shard's kind cluster, compose project and run directory.
	Name          string
	Builds        bool
	Observability bool
	RegistryToken bool
}

const (
	portRangeStart = 20000
	portBlocks     = 1000
	portBlockSize  = 10
	setupTimeout   = 30 * time.Minute
	defaultVersion = "e2e"
)

var runIDPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,14}[a-z0-9])?$`)

// Platform is one running install and the clients its tests use.
type Platform struct {
	Features
	*Cluster

	// Namespace is the namespace the worker chart, controller and agent use.
	Namespace string
	// BuildNamespace is the namespace build Jobs run in.
	BuildNamespace string
	// Domain is the platform domain the Gateway serves.
	Domain string
	// TargetPlatform is read from the kind worker nodes, independently of the API host.
	TargetPlatform string
	// Region is the region of the run's one cluster.
	Region string

	APIURL      string
	ObsProxyURL string
	OTLPURL     string
	GatewayURL  string
	RegistryURL string
	// RegistryHost is the registry as the kind nodes and builds reach it.
	RegistryHost string
	// RegistryRealm is the token endpoint the registry sends clients to in token mode.
	RegistryRealm   string
	RegistryService string
	S3Endpoint      string
	// PublicImage runs a web server on 8080; AlternatePortImage runs it on 9090.
	// CrashingImage exits a few seconds after it starts without listening; WorkerImage never listens.
	PublicImage        string
	AlternatePortImage string
	CrashingImage      string
	WorkerImage        string

	// User and Admin call the API as the application user and the configured system admin.
	User      *rpc.Clients
	Admin     *rpc.Clients
	UserToken string
	// AdminToken is the system admin's session token.
	AdminToken string
	UserID     string
	// ClusterID and ClusterKey identify the run's enrolled cluster; JoinToken enrolled it.
	ClusterID  string
	ClusterKey string
	JoinToken  string

	DB *pgxpool.Pool

	binDir         string
	logDir         string
	composeProject string
	subnet         string
	ports          ports
	values         values
	adminHome      string
	userSession    session
	adminSession   session
	processes      processes
	agentLog       *agentLog
	runCtx         context.Context
	databaseURL    string
	transitAddr    string
	buildEgress    []egressRule
	agentEnv       []corev1.EnvVar
}

type ports struct {
	api, obsProxy, s3, clickhouse, gateway, apiReplica, otlpGRPC, otlpHTTP, registry, auth int
}

// New reads the run's configuration for a shard. It starts nothing.
func New(features Features) *Platform {
	c := NewCluster(features.Name)
	base := portRangeStart + int(hash(c.run)%portBlocks)*portBlockSize
	p := &Platform{
		Features:       features,
		Cluster:        c,
		binDir:         filepath.Join(c.dir, "bin"),
		logDir:         filepath.Join(c.dir, "logs"),
		composeProject: "loco-e2e-" + c.run,
		subnet:         envconfig.String("LOCO_TEST_SUBNET", ""),
		adminHome:      filepath.Join(c.dir, "admin-home"),
		ports: ports{
			api: base, obsProxy: base + 1, s3: base + 2, clickhouse: base + 3, gateway: base + 4,
			apiReplica: base + 5, otlpGRPC: base + 6, otlpHTTP: base + 7, registry: base + 8, auth: base + 9,
		},
	}
	p.values = readValues(c.root)
	p.Namespace = locoNamespace
	p.BuildNamespace = p.values.buildNamespace
	p.Domain = platformDomain
	p.Region = clusterRegion
	p.APIURL = "http://127.0.0.1:" + strconv.Itoa(p.ports.api)
	p.ObsProxyURL = "http://127.0.0.1:" + strconv.Itoa(p.ports.obsProxy)
	p.OTLPURL = "http://127.0.0.1:" + strconv.Itoa(p.ports.otlpHTTP)
	p.GatewayURL = "http://127.0.0.1:" + strconv.Itoa(p.ports.gateway)
	p.RegistryURL = "http://127.0.0.1:" + strconv.Itoa(p.ports.registry)
	p.RegistryHost = p.registryAlias() + ":5000"
	p.RegistryService = p.registryAlias()
	p.RegistryRealm = "http://" + p.realmContainer() + ":" + strconv.Itoa(p.ports.api) + "/registry/token"
	p.S3Endpoint = "http://" + p.s3Alias() + ":" + strconv.Itoa(p.ports.s3)
	p.env = p.commandEnv()
	return p
}

// Run brings the platform up, runs the package's tests, checks the agent log of the whole run
// and tears the platform down, or leaves it running with E2E_KEEP. It returns the exit code.
func (p *Platform) Run(m *testing.M) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	p.runCtx = ctx
	if envBool("E2E_TEARDOWN_ONLY") {
		if err := p.teardown(ctx); err != nil {
			logf("teardown: %v", err)
			return 1
		}
		return 0
	}
	code := p.runTests(ctx, m)
	p.stopProcesses()
	if p.DB != nil {
		p.DB.Close()
	}
	if p.keep {
		logf(
			"Leaving run %s running (cluster %s, compose project %s); rerun it with mise run e2e --keep --skip-build %[4]s "+
				"and remove it with mise run e2e:teardown %[4]s",
			p.run,
			p.cluster,
			p.composeProject,
			p.Name,
		)
		return code
	}
	if err := p.teardown(context.WithoutCancel(ctx)); err != nil {
		logf("teardown: %v", err)
		return 1
	}
	return code
}

func (p *Platform) runTests(ctx context.Context, m *testing.M) int {
	setupCtx, cancel := context.WithTimeout(ctx, setupTimeout)
	err := p.up(setupCtx)
	cancel()
	if err != nil {
		logf("setup of run %s failed: %v", p.run, err)
		p.dumpCluster(ctx)
		p.dump(context.WithoutCancel(ctx))
		return 1
	}
	logf("Platform %s ready", p.run)
	code := m.Run()
	if err := p.agentLog.check(); err != nil {
		logf("%v", err)
		code = 1
	}
	if code != 0 {
		p.dump(context.WithoutCancel(ctx))
	}
	return code
}

func hash(s string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(s))
	return h.Sum32()
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func envBool(name string) bool {
	value, err := strconv.ParseBool(envOr(name, "false"))
	if err != nil {
		panic(fmt.Sprintf("%s must be true or false: %v", name, err))
	}
	return value
}

func logf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "%s %s\n", time.Now().Format(time.TimeOnly), fmt.Sprintf(format, args...))
}

var errNotYet = errors.New("not yet")
