package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/sync/errgroup"
)

const (
	readyTimeout     = 2 * time.Minute
	rolloutTimeout   = 10 * time.Minute
	gatewayHTTPPort  = 80
	clickhouseNative = 9000
	clickhouseUser   = "loco_e2e"
	clickhousePass   = "loco_e2e_pass"   //nolint:gosec // the throwaway ClickHouse of the run
	gatewayTLSSecret = "e2e-gateway-tls" //nolint:gosec // a Secret name, not a credential
)

// up reserves the Compose network, then builds and starts the platform.
func (p *Platform) up(ctx context.Context) error {
	for _, dir := range []string{p.binDir, p.logDir, p.adminHome} {
		if err := mkdirReadable(dir); err != nil {
			return err
		}
	}
	if _, err := p.mise(ctx, nil, "doctor"); err != nil {
		return err
	}
	if err := p.checkPorts(ctx); err != nil {
		return err
	}
	if err := p.reserveComposeNetwork(ctx); err != nil {
		return err
	}
	if _, err := p.Command(
		ctx,
		"go",
		"-C",
		p.root,
		"build",
		"-o",
		filepath.Join(p.binDir, "pullimage"),
		"./tools/pullimage",
	); err != nil {
		return err
	}
	p.agentLog = &agentLog{path: p.logPath("agent"), expected: map[string]bool{}}
	if err := os.Remove(p.agentLog.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}

	logf(
		"Run %s: building images and binaries, starting kind cluster %s and compose project %s",
		p.run,
		p.cluster,
		p.composeProject,
	)
	images := newImages(p)
	group, groupCtx := errgroup.WithContext(ctx)
	group.Go(func() error { return images.build(groupCtx) })
	group.Go(func() error { return p.setupClusterSide(groupCtx) })
	group.Go(func() error { return p.setupDataServices(groupCtx) })
	if err := group.Wait(); err != nil {
		return err
	}
	logf("Loading images and installing the loco-worker chart")
	if err := images.load(ctx); err != nil {
		return err
	}
	if err := p.installWorker(ctx, images, false); err != nil {
		return err
	}
	if err := p.resetClusterState(ctx); err != nil {
		return err
	}
	logf("Starting the API, signing in and enrolling the cluster")
	if err := p.StartAPI(ctx); err != nil {
		return err
	}
	if p.RegistryToken {
		if err := p.startTokenRegistry(ctx); err != nil {
			return err
		}
	}
	if err := p.signIn(ctx); err != nil {
		return err
	}
	if err := p.enrollCluster(ctx); err != nil {
		return err
	}
	if p.Observability {
		if err := p.installWorker(ctx, images, true); err != nil {
			return err
		}
		if err := p.forwardGateway(ctx); err != nil {
			return err
		}
	}
	if err := p.startObsProxy(ctx); err != nil {
		return err
	}
	if p.Observability {
		return p.startIngest(ctx, images)
	}
	return nil
}

func (p *Platform) checkPorts(ctx context.Context) error {
	required := map[string]int{
		"API": p.ports.api, "API replica": p.ports.apiReplica, "observability proxy": p.ports.obsProxy,
		"OTLP/gRPC ingest": p.ports.otlpGRPC, "OTLP/HTTP ingest": p.ports.otlpHTTP,
	}
	if p.Observability {
		required["gateway"] = p.ports.gateway
	}
	for purpose, port := range required {
		dialer := net.Dialer{Timeout: time.Second}
		conn, err := dialer.DialContext(ctx, "tcp", "127.0.0.1:"+strconv.Itoa(port))
		if err == nil {
			return errors.Join(conn.Close(), fmt.Errorf(
				"port %d (%s) is in use; set E2E_RUN_ID to move run %s to another port block",
				port,
				purpose,
				p.run,
			))
		}
	}
	return nil
}

// setupClusterSide creates or reuses the kind cluster and starts what runs on the kind network:
// ClickHouse, the registry, the source bucket and the public fixture images.
func (p *Platform) setupClusterSide(ctx context.Context) error {
	if err := p.setupKind(ctx); err != nil {
		return err
	}
	if _, err := p.Kubectl(
		ctx,
		"apply",
		"-f",
		filepath.Join(p.root, "manifests", "namespaces", locoNamespace+".yaml"),
	); err != nil {
		return err
	}
	if _, err := p.mise(ctx, nil, "helm:deps"); err != nil {
		return err
	}
	crds := []string{"crds:apply"}
	if !p.Builds {
		crds = append(crds, "--without-builds")
	}
	if _, err := p.mise(ctx, []string{"KIND_CLUSTER=" + p.cluster}, crds...); err != nil {
		return err
	}
	if err := p.pullComposeImages(ctx, "registry", "s3"); err != nil {
		return err
	}
	if err := p.setupClickHouse(ctx); err != nil {
		return err
	}
	if err := p.setupRegistry(ctx); err != nil {
		return err
	}
	return p.setupPublicImages(ctx)
}

func (p *Platform) reserveComposeNetwork(ctx context.Context) error {
	name := p.composeProject + "_default"
	if _, err := p.Command(ctx, "docker", "network", "inspect", name); err == nil {
		return nil
	} else if !strings.Contains(err.Error(), "No such network") {
		return err
	}
	args := []string{"network", "create",
		"--label", "com.docker.compose.project=" + p.composeProject,
		"--label", "com.docker.compose.network=default"}
	if p.subnet != "" {
		args = append(args, "--subnet", p.subnet)
	}
	if _, err := p.Command(ctx, "docker", append(args, name)...); err != nil {
		return fmt.Errorf(
			"reserve network before building: %w; select an unused LOCO_TEST_SUBNET or inspect retained runs with "+
				"docker network ls --filter label=com.docker.compose.project; "+
				"remove this run with mise run e2e:teardown %s",
			err, p.Name,
		)
	}
	return nil
}

// setupDataServices starts Postgres with a fresh schema, the identity provider and OpenBao.
func (p *Platform) setupDataServices(ctx context.Context) error {
	if err := p.pullComposeImages(ctx, "postgres", "openbao", "dex"); err != nil {
		return err
	}
	if _, err := p.compose(ctx, "up", "-d", "--wait", "--pull", "never", "postgres", "dex", "openbao"); err != nil {
		return err
	}
	port, err := p.composePort(ctx, "postgres", 5432)
	if err != nil {
		return err
	}
	p.databaseURL = fmt.Sprintf(
		"postgres://%s:%s@127.0.0.1:%d/%s?sslmode=disable",
		postgresUser,
		postgresPassword,
		port,
		postgresDB,
	)
	pool, err := pgxpool.New(ctx, p.databaseURL)
	if err != nil {
		return err
	}
	p.DB = pool
	if err = waitFor(ctx, readyTimeout, "Postgres to answer queries from the host", func(ctx context.Context) error {
		return pool.Ping(ctx)
	}); err != nil {
		return err
	}
	if _, err = pool.Exec(ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public;"); err != nil {
		return fmt.Errorf("reset the database: %w", err)
	}
	if _, err = p.Command(
		ctx,
		"goose",
		"-dir",
		filepath.Join(p.root, "api", "migrations"),
		"postgres",
		p.databaseURL,
		"up",
	); err != nil {
		return err
	}
	if err = waitFor(ctx, readyTimeout, "the identity provider's discovery document", func(ctx context.Context) error {
		return httpOK(ctx, p.authIssuer()+"/.well-known/openid-configuration")
	}); err != nil {
		return err
	}
	if _, err = p.compose(ctx, "run", "--pull", "never", "--rm", "openbao-seed"); err != nil {
		return err
	}
	openbao, err := p.composePort(ctx, "openbao", 8200)
	if err != nil {
		return err
	}
	p.transitAddr = "http://127.0.0.1:" + strconv.Itoa(openbao)
	return nil
}

func (p *Platform) pullComposeImages(ctx context.Context, services ...string) error {
	out, err := p.compose(ctx, append([]string{"config", "--images"}, services...)...)
	if err != nil {
		return err
	}
	return p.pullImages(ctx, strings.Fields(out)...)
}

// pullImages pulls images the Docker host lacks, retrying registry errors.
func (p *Platform) pullImages(ctx context.Context, images ...string) error {
	_, err := p.Command(ctx, filepath.Join(p.binDir, "pullimage"), images...)
	return err
}

func (p *Platform) setupClickHouse(ctx context.Context) error {
	keeper := filepath.Join(p.logDir, "clickhouse-keeper.xml")
	if err := writeReadable(keeper, []byte("<clickhouse>"+p.values.clickhouseKeeper+"</clickhouse>\n")); err != nil {
		return err
	}
	if err := p.pullImages(ctx, p.values.clickhouseImage); err != nil {
		return err
	}
	if err := p.removeContainers(ctx, p.clickhouseName()); err != nil {
		return err
	}
	if _, err := p.Command(ctx, "docker", "run", "-d", "--name", p.clickhouseName(), "--network", "kind",
		"-p", fmt.Sprintf("127.0.0.1:%d:%d", p.ports.clickhouse, clickhouseNative),
		"-e", "CLICKHOUSE_USER="+clickhouseUser, "-e", "CLICKHOUSE_PASSWORD="+clickhousePass,
		"-v", keeper+":/etc/clickhouse-server/config.d/keeper.xml:ro",
		p.values.clickhouseImage); err != nil {
		return err
	}
	return waitFor(ctx, readyTimeout, "ClickHouse to answer queries", func(ctx context.Context) error {
		_, err := p.ClickHouseClient(ctx, "SELECT 1")
		return err
	})
}

// ClickHouseClient runs a query with clickhouse-client in the run's ClickHouse container.
func (p *Platform) ClickHouseClient(ctx context.Context, query string, args ...string) (string, error) {
	return p.Command(ctx, "docker", append([]string{"exec", p.clickhouseName(), "clickhouse-client",
		"--user", clickhouseUser, "--password", clickhousePass, "-q", query}, args...)...)
}

func (p *Platform) setupRegistry(ctx context.Context) error {
	if p.clusterReused {
		// The registry's tags are immutable, so a reused run starts from an empty one.
		if _, err := p.compose(ctx, "rm", "--stop", "--force", "registry"); err != nil {
			return err
		}
		if _, err := p.Command(
			ctx,
			"docker",
			"volume",
			"rm",
			"--force",
			p.composeProject+"_registry-data",
		); err != nil {
			return err
		}
	}
	egress, err := p.mise(
		ctx,
		[]string{"LOCO_REGISTRY_HOST=" + p.RegistryHost, "LOCO_SOURCE_BUCKET_ENDPOINT=" + p.S3Endpoint},
		"cluster:build-egress",
	)
	if err != nil {
		return err
	}
	var rules []egressRule
	if err := json.Unmarshal([]byte(egress), &rules); err != nil {
		return fmt.Errorf("decode the build egress: %w", err)
	}
	if !p.RegistryToken {
		if _, err := p.compose(ctx, "up", "-d", "--wait", "--pull", "never", "registry", "s3"); err != nil {
			return err
		}
		p.buildEgress = rules
		return p.connectRegistry(ctx)
	}
	if _, err := p.compose(ctx, "up", "-d", "--wait", "--pull", "never", "s3"); err != nil {
		return err
	}
	// Builds reach the token endpoint through the realm proxy on the API's port.
	cidrs := map[string]bool{}
	for _, rule := range rules {
		if !cidrs[rule.CIDR] {
			cidrs[rule.CIDR] = true
			rules = append(rules, egressRule{CIDR: rule.CIDR, Port: p.ports.api})
		}
	}
	p.buildEgress = rules
	return p.startRealmProxy(ctx)
}

type egressRule struct {
	CIDR string `json:"cidr"`
	Port int    `json:"port"`
}

func (p *Platform) connectRegistry(ctx context.Context) error {
	auth := "static"
	if p.RegistryToken {
		auth = "token"
	}
	_, err := p.mise(ctx, []string{
		"KIND_CLUSTER=" + p.cluster,
		"COMPOSE_PROJECT_NAME=" + p.composeProject,
		"REGISTRY_PORT=" + strconv.Itoa(p.ports.registry),
		"REGISTRY_ALIAS=" + p.registryAlias(),
		"REGISTRY_AUTH=" + auth,
		"S3_ALIAS=" + p.s3Alias(),
	}, "cluster:registry")
	return err
}

// startRealmProxy puts the API's token endpoint on the kind network under a DNS name.
func (p *Platform) startRealmProxy(ctx context.Context) error {
	if err := mkdirReadable(p.registryTokenDir()); err != nil {
		return err
	}
	conf := filepath.Join(p.registryTokenDir(), "realm.conf")
	nginx := fmt.Sprintf(
		"server {\n    listen %d;\n    location / {\n        proxy_pass http://host.docker.internal:%d;\n    }\n}\n",
		p.ports.api,
		p.ports.api,
	)
	if err := writeReadable(conf, []byte(nginx)); err != nil {
		return err
	}
	image := fromImage(p.root, "tests/e2e/fixtures/public-image/Dockerfile")
	if err := p.pullImages(ctx, image); err != nil {
		return err
	}
	if err := p.removeContainers(ctx, p.realmContainer()); err != nil {
		return err
	}
	_, err := p.Command(ctx, "docker", "run", "-d", "--name", p.realmContainer(), "--network", "kind",
		"--add-host", "host.docker.internal:host-gateway", "-v", conf+":/etc/nginx/conf.d/default.conf:ro", image)
	return err
}

// startTokenRegistry starts the registry with the API's token endpoint and signing key, which
// exist once the API runs.
func (p *Platform) startTokenRegistry(ctx context.Context) error {
	if err := waitFor(ctx, readyTimeout, "the realm proxy to reach the API", func(ctx context.Context) error {
		_, err := p.Command(ctx, "docker", "exec", p.realmContainer(), "wget", "-q", "-O", "/dev/null",
			"http://127.0.0.1:"+strconv.Itoa(p.ports.api)+"/health")
		return err
	}); err != nil {
		return err
	}
	key, err := httpGet(ctx, p.APIURL+"/.well-known/loco-registry-key.pem")
	if err != nil {
		return err
	}
	keyPath := filepath.Join(p.registryTokenDir(), "key.pem")
	if err = writeReadable(keyPath, key); err != nil {
		return err
	}
	var config map[string]any
	data, err := os.ReadFile(filepath.Join(p.root, "dev", "registry", "config.json"))
	if err != nil {
		return err
	}
	if err = json.Unmarshal(data, &config); err != nil {
		return err
	}
	httpConfig, ok := config["http"].(map[string]any)
	if !ok {
		return errors.New("dev/registry/config.json has no http section")
	}
	auth, ok := httpConfig["auth"].(map[string]any)
	if !ok {
		return errors.New("dev/registry/config.json has no http.auth section")
	}
	httpConfig["auth"] = map[string]any{
		"bearer":    map[string]any{"realm": p.RegistryRealm, "service": p.RegistryService, "cert": keyPath},
		"failDelay": auth["failDelay"],
	}
	delete(httpConfig, "accessControl")
	tokenConfig, err := json.Marshal(config)
	if err != nil {
		return err
	}
	if err := writeReadable(filepath.Join(p.registryTokenDir(), "config.json"), tokenConfig); err != nil {
		return err
	}
	if _, err := p.compose(ctx, "up", "-d", "--wait", "--pull", "never", "--force-recreate", "registry"); err != nil {
		return err
	}
	return p.connectRegistry(ctx)
}

// setupPublicImages mirrors the public fixture image into the run's anonymous registry, with a
// crashing and a worker variant.
func (p *Platform) setupPublicImages(ctx context.Context) error {
	if _, err := p.compose(ctx, "up", "-d", "public-image-registry"); err != nil {
		return err
	}
	port, err := p.composePort(ctx, "public-image-registry", 5000)
	if err != nil {
		return err
	}
	host := "localhost:" + strconv.Itoa(port)
	if err = waitFor(ctx, readyTimeout, "the public fixture registry", func(ctx context.Context) error {
		return httpOK(ctx, "http://"+host+"/v2/")
	}); err != nil {
		return err
	}
	source := fromImage(p.root, "tests/e2e/fixtures/public-image/Dockerfile")
	p.PublicImage = host + "/public-image:e2e"
	p.AlternatePortImage = host + "/public-image:e2e-port-9090"
	p.CrashingImage = host + "/public-image:e2e-crashing"
	p.WorkerImage = host + "/public-image:e2e-worker"
	if err = mirrorImage(ctx, source, p.PublicImage, nil); err != nil {
		return err
	}
	if err = mirrorImage(ctx, p.PublicImage, p.AlternatePortImage, []string{"sh", "-ec",
		`sed 's@/etc/nginx/conf.d/\*.conf@/tmp/e2e-server.conf@' /etc/nginx/nginx.conf > /tmp/e2e-nginx.conf
sed 's/8080/9090/g' /etc/nginx/conf.d/default.conf > /tmp/e2e-server.conf
exec nginx -c /tmp/e2e-nginx.conf -g 'daemon off;'`,
	}); err != nil {
		return err
	}
	if err = mirrorImage(ctx, p.PublicImage, p.CrashingImage, []string{"sh", "-c", "sleep 5; exit 1"}); err != nil {
		return err
	}
	if err = mirrorImage(
		ctx,
		p.PublicImage,
		p.WorkerImage,
		[]string{"sh", "-c", `trap "exit 0" TERM; while :; do sleep 1; done`},
	); err != nil {
		return err
	}
	hosts := fmt.Sprintf(
		"[host.\"http://%s-public-image:5000\"]\n  capabilities = [\"pull\", \"resolve\"]\n",
		p.composeProject,
	)
	if _, err = p.Command(
		ctx,
		"docker",
		"exec",
		p.Node(),
		"mkdir",
		"-p",
		"/etc/containerd/certs.d/"+host,
	); err != nil {
		return err
	}
	_, err = p.commandStdin(
		ctx,
		[]byte(hosts),
		"docker",
		"exec",
		"-i",
		p.Node(),
		"tee",
		"/etc/containerd/certs.d/"+host+"/hosts.toml",
	)
	return err
}

// resetClusterState removes what a previous run of a reused cluster left.
func (p *Platform) resetClusterState(ctx context.Context) error {
	if !p.clusterReused {
		return nil
	}
	if _, err := p.Kubectl(
		ctx,
		"delete",
		"applications.infra.loco.io",
		"--all",
		"--all-namespaces",
		"--cascade=foreground",
		"--wait",
	); err != nil {
		return err
	}
	if exists, err := p.Exists(ctx, "crd", "builds.infra.loco.io"); err != nil {
		return err
	} else if exists {
		if _, err := p.Kubectl(
			ctx,
			"delete",
			"builds.infra.loco.io",
			"--all",
			"--all-namespaces",
			"--cascade=foreground",
			"--wait",
		); err != nil {
			return err
		}
	}
	out, err := p.Kubectl(ctx, "get", "namespaces", "-o", "name")
	if err != nil {
		return err
	}
	var stale []string
	for namespace := range strings.FieldsSeq(out) {
		name := strings.TrimPrefix(namespace, "namespace/")
		if strings.HasPrefix(name, p.values.workspacePrefix) || strings.HasPrefix(name, "e2e-") {
			stale = append(stale, namespace)
		}
	}
	if len(stale) > 0 {
		if _, err = p.Kubectl(ctx, append([]string{"delete", "--wait"}, stale...)...); err != nil {
			return err
		}
	}
	_, err = p.Kubectl(
		ctx,
		"-n",
		locoNamespace,
		"delete",
		"secrets",
		"-l",
		"loco.io/registry-pull",
		"--ignore-not-found",
	)
	return err
}

// forwardGateway forwards the Envoy proxy Service to the host for requests from the tests.
func (p *Platform) forwardGateway(ctx context.Context) error {
	service, err := p.Kubectl(ctx, "-n", locoNamespace, "get", "services", "-l", gatewaySelector, "-o", "name")
	if err != nil {
		return err
	}
	if err := p.startProcess(
		"gateway-forward",
		nil,
		"kubectl",
		"--context",
		p.kubeContext,
		"-n",
		locoNamespace,
		"port-forward",
		"--address",
		"127.0.0.1",
		strings.TrimSpace(service),
		fmt.Sprintf("%d:%d", p.ports.gateway, gatewayHTTPPort),
	); err != nil {
		return err
	}
	return waitFor(ctx, readyTimeout, "the gateway port-forward", func(ctx context.Context) error {
		conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", "127.0.0.1:"+strconv.Itoa(p.ports.gateway))
		if err != nil {
			return err
		}
		return conn.Close()
	})
}

const gatewaySelector = "gateway.envoyproxy.io/owning-gateway-name=" + gatewayName +
	",gateway.envoyproxy.io/owning-gateway-namespace=" + locoNamespace

// GatewayService is the Envoy proxy Service of the platform Gateway.
func (p *Platform) GatewayService(ctx context.Context) (string, error) {
	out, err := p.Kubectl(
		ctx,
		"-n",
		locoNamespace,
		"get",
		"services",
		"-l",
		gatewaySelector,
		"-o",
		"jsonpath={.items[0].metadata.name}",
	)
	return strings.TrimSpace(out), err
}

func httpOK(ctx context.Context, url string) error {
	_, err := httpGet(ctx, url)
	return err
}

var errHTTPStatus = errors.New("unexpected HTTP status")

func httpGet(ctx context.Context, url string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w %d from %s", errHTTPStatus, response.StatusCode, url)
	}
	return body, nil
}

// writeReadable writes a file a container reads as another user.
func writeReadable(path string, data []byte) error {
	return os.WriteFile(path, data, 0o644) //nolint:gosec // a container reads it as another user
}

// mkdirReadable creates a directory a container reads as another user.
func mkdirReadable(path string) error {
	return os.MkdirAll(path, 0o755) //nolint:gosec // a container reads it as another user
}

// removeContainers removes containers that may not exist.
func (p *Platform) removeContainers(ctx context.Context, names ...string) error {
	_, err := p.Command(ctx, "docker", append([]string{"rm", "-fv"}, names...)...)
	if err != nil && !strings.Contains(err.Error(), "No such container") {
		return err
	}
	return nil
}
