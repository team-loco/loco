package platform

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/jackc/pgx/v5"
)

const (
	agentHeartbeatInterval          = "10s"
	imageRetention                  = "1"
	imageSweepInterval              = "5s"
	registryTokenKeyVersion         = "1"
	registryAccessTokenLifetime     = "1m"
	registryPullCredentialLifetime  = "1m"
	registryCredentialCheckInterval = "10s"
	// SystemAdmin is the admin the API's LOCO_SYSTEM_ADMINS lists.
	SystemAdmin = "loco-admin@test.local"
	// DashboardOrigin is the browser origin the API allows.
	DashboardOrigin = "https://dashboard.e2e.test"
	apiProcess      = "api"
	replicaProcess  = "api-replica"
	agentProcess    = "agent"
	obsProcess      = "obs-proxy"
)

// StartAPI starts the API on its port and waits for its health check.
func (p *Platform) StartAPI(ctx context.Context) error {
	return p.startAPI(ctx, apiProcess, p.ports.api)
}

// StopAPI stops the API process.
func (p *Platform) StopAPI() {
	p.stopProcess(apiProcess)
}

// StartAPIReplica starts a second API process on the same database and returns its URL.
func (p *Platform) StartAPIReplica(ctx context.Context) (string, error) {
	return "http://127.0.0.1:" + strconv.Itoa(p.ports.apiReplica), p.startAPI(ctx, replicaProcess, p.ports.apiReplica)
}

// StopAPIReplica stops the second API process.
func (p *Platform) StopAPIReplica() {
	p.stopProcess(replicaProcess)
}

func (p *Platform) startAPI(ctx context.Context, name string, port int) error {
	dev, err := readEnvFile(filepath.Join(p.root, "dev", "api.env"))
	if err != nil {
		return err
	}
	bind := "127.0.0.1"
	if p.Observability || p.RegistryToken {
		// The ingest container and the realm proxy reach the API from the kind network.
		bind = "0.0.0.0"
	}
	env := []string{
		"LOCO_TARGET_PLATFORM=" + p.TargetPlatform,
		`AUTH_ISSUERS=[{"issuer":"` + p.authIssuer() + `","audience":"loco-e2e"}]`,
		"AUTH_SIGNUP_MODE=open",
		"LOCO_SYSTEM_ADMINS=" + SystemAdmin,
		// Every test tenant is an organization of the one e2e user.
		"LOCO_LIMIT_ORGS_PER_USER=50",
		"DATABASE_URL=" + p.databaseURL,
		"CACHE_TYPE=in-memory",
		"APP_PORT=" + bind + ":" + strconv.Itoa(port),
		"CLICKHOUSE_URL=" + p.clickhouseDSN(),
		"CLICKHOUSE_DB=" + p.values.clickhouseDB,
		"CLICKHOUSE_QUERY_TIMEOUT=5s",
		"LOCO_OBSERVABILITY_DEFAULT_LIMIT=100",
		"LOCO_OBSERVABILITY_MAX_LIMIT=1000",
		"LOCO_OBSERVABILITY_MAX_TAIL_DURATION=10m",
		"LOCO_AGENT_HEARTBEAT_INTERVAL=" + agentHeartbeatInterval,
		"CORS_ALLOWED_ORIGINS=" + DashboardOrigin,
		"APP_ENV=test",
		"LOG_LEVEL=debug",
		"LOCO_REGISTRY_HOST=" + p.RegistryHost,
		"LOCO_REGISTRY_URL=" + p.RegistryURL,
		"LOCO_IMAGE_RETENTION=" + imageRetention,
		"LOCO_IMAGE_SWEEP_INTERVAL=" + imageSweepInterval,
		"LOCO_SOURCE_BUCKET=" + sourceBucket,
		"LOCO_SOURCE_BUCKET_ACCESS_KEY_ID=" + sourceAccessKey,
		"LOCO_SOURCE_BUCKET_SECRET_ACCESS_KEY=" + sourceSecretKey,
		"LOCO_SOURCE_BUCKET_REGION=" + clusterRegion,
		"LOCO_SOURCE_BUCKET_ENDPOINT=" + p.S3Endpoint,
		"LOCO_SOURCE_BUCKET_FORCE_PATH_STYLE=true",
		"LOCO_SECRETS_TRANSIT_ADDR=" + p.transitAddr,
		"LOCO_SECRETS_TRANSIT_MOUNT=" + dev["LOCO_SECRETS_TRANSIT_MOUNT"],
		"LOCO_SECRETS_TRANSIT_KEY=" + dev["LOCO_SECRETS_TRANSIT_KEY"],
		"LOCO_SECRETS_TRANSIT_AUTH=" + dev["LOCO_SECRETS_TRANSIT_AUTH"],
		"LOCO_SECRETS_TRANSIT_TOKEN=" + dev["LOCO_SECRETS_TRANSIT_TOKEN"],
		"LOCO_INGEST_TRANSIT_KEY=" + dev["LOCO_INGEST_TRANSIT_KEY"],
	}
	if p.RegistryToken {
		env = append(env,
			"LOCO_REGISTRY_AUTH=token",
			"LOCO_REGISTRY_TRANSIT_KEY="+dev["LOCO_REGISTRY_TRANSIT_KEY"],
			"LOCO_REGISTRY_TRANSIT_KEY_VERSION="+registryTokenKeyVersion,
			"LOCO_REGISTRY_TOKEN_SERVICE="+p.RegistryService,
			"LOCO_REGISTRY_ACCESS_TOKEN_LIFETIME="+registryAccessTokenLifetime,
			"LOCO_REGISTRY_PULL_CREDENTIAL_LIFETIME="+registryPullCredentialLifetime)
	} else {
		env = append(
			env,
			"LOCO_REGISTRY_USERNAME="+RegistryAPIUser.Name,
			"LOCO_REGISTRY_PASSWORD="+RegistryAPIUser.Password,
		)
	}
	if err := p.startProcess(name, env, filepath.Join(p.binDir, "loco-api")); err != nil {
		return err
	}
	return waitFor(ctx, readyTimeout, name+" health", func(ctx context.Context) error {
		return httpOK(ctx, "http://127.0.0.1:"+strconv.Itoa(port)+"/health")
	})
}

func (p *Platform) clickhouseDSN() string {
	return fmt.Sprintf("clickhouse://%s:%s@127.0.0.1:%d", clickhouseUser, clickhousePass, p.ports.clickhouse)
}

// StartAgent starts the agent against apiURL, enrolling with joinToken when it is set, and waits
// until the cluster records a new sync generation from this agent's version.
func (p *Platform) StartAgent(ctx context.Context, apiURL, joinToken string) error {
	return p.startAgent(ctx, apiURL, joinToken, "")
}

// StartAgentWithTokenFile waits for enrollment from a token delivered through a file.
func (p *Platform) StartAgentWithTokenFile(ctx context.Context, apiURL, tokenFile string) error {
	return p.startAgent(ctx, apiURL, "", tokenFile)
}

func (p *Platform) startAgent(ctx context.Context, apiURL, joinToken, tokenFile string) error {
	var previous int64
	err := p.DB.QueryRow(ctx, "SELECT sync_generation FROM clusters WHERE cluster_key = $1", p.ClusterKey).
		Scan(&previous)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	env := make([]string, 0, len(p.agentEnv)+5)
	for _, variable := range p.agentEnv {
		env = append(env, variable.Name+"="+variable.Value)
	}
	env = append(env,
		"CONTROL_PLANE_URL="+apiURL,
		"LOCO_JOIN_TOKEN="+joinToken,
		"LOCO_JOIN_TOKEN_FILE="+tokenFile,
		"LOCO_NAMESPACE="+locoNamespace,
		"LOG_LEVEL="+p.values.agentLogLevel,
		"KUBECONFIG="+p.kubeconfig,
	)
	if err := p.startProcess(agentProcess, env, filepath.Join(p.binDir, "loco-agent")); err != nil {
		return err
	}
	return p.WaitAgentRegistered(ctx, previous)
}

// WaitAgentRegistered waits until the agent records a sync generation after previous.
func (p *Platform) WaitAgentRegistered(ctx context.Context, previous int64) error {
	return waitFor(ctx, readyTimeout, "agent registration", func(ctx context.Context) error {
		var registered bool
		err := p.DB.QueryRow(
			ctx,
			"SELECT EXISTS (SELECT 1 FROM clusters WHERE cluster_key = $1 AND agent_version = $2 AND sync_generation > $3)",
			p.ClusterKey,
			p.Version,
			previous,
		).Scan(&registered)
		if err == nil && !registered {
			return errNotYet
		}
		return err
	})
}

// RestartAgent stops the agent and starts it again against the API without a join token.
func (p *Platform) RestartAgent(ctx context.Context) error {
	p.StopAgent()
	return p.StartAgent(ctx, p.APIURL, "")
}

// StopAgent stops the agent.
func (p *Platform) StopAgent() {
	p.stopProcess(agentProcess)
}

// AgentExited returns the agent's exit once it exits by itself, or nil while it runs.
func (p *Platform) AgentExited() (exited bool, code int) {
	p.processes.mu.Lock()
	proc := p.processes.running[agentProcess]
	p.processes.mu.Unlock()
	if proc == nil {
		return false, 0
	}
	select {
	case <-proc.done:
		return true, proc.cmd.ProcessState.ExitCode()
	default:
		return false, 0
	}
}

// SignalAgent sends a signal to the agent, such as SIGSTOP to hold it and SIGCONT to resume it.
func (p *Platform) SignalAgent(signal syscall.Signal) error {
	p.processes.mu.Lock()
	proc := p.processes.running[agentProcess]
	p.processes.mu.Unlock()
	if proc == nil {
		return errors.New("the agent is not running")
	}
	return proc.cmd.Process.Signal(signal)
}

func (p *Platform) startObsProxy(ctx context.Context) error {
	dsn := p.clickhouseDSN()
	if err := p.startProcess(obsProcess, []string{
		"PORT=" + strconv.Itoa(p.ports.obsProxy),
		"CONTROL_PLANE_URL=" + p.APIURL,
		"CLICKHOUSE_DB=" + p.values.clickhouseDB,
		"CLICKHOUSE_MIGRATOR_URL=" + dsn,
		"CLICKHOUSE_INGEST_URL=" + dsn,
		"OTLP_GRPC_PORT=" + strconv.Itoa(p.ports.otlpGRPC),
		"OTLP_HTTP_PORT=" + strconv.Itoa(p.ports.otlpHTTP),
	}, filepath.Join(p.binDir, "loco-obs-proxy")); err != nil {
		return err
	}
	return waitFor(ctx, readyTimeout, "observability proxy readiness", func(ctx context.Context) error {
		return httpOK(ctx, p.ObsProxyURL+"/readyz")
	})
}

// startIngest starts a second observability proxy on the kind network for the collectors.
func (p *Platform) startIngest(ctx context.Context, images *images) error {
	if err := p.removeContainers(ctx, p.ingestContainer()); err != nil {
		return err
	}
	native := "clickhouse://" + clickhouseUser + ":" + clickhousePass + "@" +
		net.JoinHostPort(p.clickhouseName(), strconv.Itoa(clickhouseNative))
	if _, err := p.Command(ctx, "docker", "run", "-d", "--name", p.ingestContainer(), "--network", "kind",
		"--add-host", "host.docker.internal:host-gateway",
		"-p", "127.0.0.1::"+strconv.Itoa(p.values.ingestPort),
		"-e", "CONTROL_PLANE_URL=http://host.docker.internal:"+strconv.Itoa(p.ports.api),
		"-e", "PORT="+strconv.Itoa(p.values.ingestPort),
		"-e", "CLICKHOUSE_DB="+p.values.clickhouseDB,
		"-e", "OTLP_GRPC_PORT="+strconv.Itoa(p.values.ingestGRPCPort),
		"-e", "OTLP_HTTP_PORT="+strconv.Itoa(p.values.ingestHTTPPort),
		"-e", "CLICKHOUSE_INGEST_URL="+native,
		"-e", "CLICKHOUSE_MIGRATOR_URL="+native,
		images.obsProxy); err != nil {
		return err
	}
	return waitFor(ctx, readyTimeout, "the kind network proxy readiness", func(ctx context.Context) error {
		published, err := p.Command(
			ctx,
			"docker",
			"port",
			p.ingestContainer(),
			strconv.Itoa(p.values.ingestPort)+"/tcp",
		)
		if err != nil {
			return err
		}
		return httpOK(ctx, "http://"+strings.TrimSpace(strings.Split(published, "\n")[0])+"/readyz")
	})
}

// IngestContainer is the Docker container of the observability proxy the collectors export to.
func (p *Platform) IngestContainer() string {
	return p.ingestContainer()
}

func readEnvFile(path string) (map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	values := map[string]string{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		name, value, ok := strings.Cut(scanner.Text(), "=")
		if ok && !strings.HasPrefix(name, "#") {
			values[name] = value
		}
	}
	return values, scanner.Err()
}
