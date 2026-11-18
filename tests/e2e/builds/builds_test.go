package builds

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/team-loco/loco/k8sapi/v1alpha1"
	"github.com/team-loco/loco/tests/e2e/platform"
)

const (
	buildTimeout = 15 * time.Minute
	// policySet is the nftables set kindnet fills with the pod IPs its network policies cover.
	policySet     = "inet kindnet-network-policies podips-v4"
	apiServerPort = "6443"
)

const (
	// succeeded is the phase of a Build that pushed its image.
	succeeded = "Succeeded"
	// buildStep is the build Job's container that runs BuildKit.
	buildStep = "build"
)

var digestPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

// TestBuilds runs Build objects through the build controller alone: a first build, a rebuild from
// its cache, an untrusted build looking for credentials, and the build namespace's egress policy.
func TestBuilds(t *testing.T) {
	t.Parallel()
	appRepository := p.RegistryHost + "/e2e/build-app"
	fixture := t.TempDir()
	p.CopyFixture(t, "build-app", fixture)
	first := applyBuild(t, "e2e-app-1", p.UploadSource(t, "e2e-app-1", fixture), buildAppDockerfile, appRepository, "")

	t.Run("the first build pushes an image and its cache", func(t *testing.T) {
		if first.Status.Phase != succeeded {
			t.Fatalf("first build phase %q (%s)", first.Status.Phase, first.Status.Message)
		}
		for name, digest := range map[string]string{"image": first.Status.ImageDigest, "cache": first.Status.CacheDigest} {
			if !digestPattern.MatchString(digest) {
				t.Errorf("first build %s digest %q is not a sha256 digest", name, digest)
			}
		}
		t.Logf("first build took %s", buildDuration(first))
	})

	t.Run("the BuildKit launcher tolerates delayed readiness and reports daemon crashes", func(t *testing.T) {
		testBuildkitStartup(t, first.Name)
	})

	t.Run("images report their build version", func(t *testing.T) {
		if logs := buildLogs(t, "e2e-app-1", "fetch"); !strings.Contains(logs, "version="+p.Version) {
			t.Errorf("builder fetch log lacks version=%s:\n%s", p.Version, logs)
		}
		logs := p.MustKubectl(t, "-n", p.Namespace, "logs", "deployment/loco-build-controller")
		if !strings.Contains(logs, `"version": "`+p.Version+`"`) {
			t.Errorf("build controller log lacks its version %s", p.Version)
		}
	})

	t.Run("only the push step holds the push credentials", func(t *testing.T) {
		var job batchv1.Job
		p.MustGet(t, &job, "-n", p.BuildNamespace, "job", "e2e-app-1")
		spec := job.Spec.Template.Spec
		if !mounts(spec.Containers, "push", "push-credentials") {
			t.Error("the push step does not mount the push credentials")
		}
		for _, container := range spec.InitContainers {
			for _, mount := range container.VolumeMounts {
				if container.Name == "build" && strings.Contains(mount.Name, "credentials") ||
					container.Name == "fetch" && mount.Name == "push-credentials" {
					t.Errorf("the %s step mounts %s", container.Name, mount.Name)
				}
			}
		}
		if spec.AutomountServiceAccountToken == nil || *spec.AutomountServiceAccountToken {
			t.Error("the build pod gets a service account token")
		}
	})

	t.Run("the nodes account pulls the image and anonymous pulls are refused", func(t *testing.T) {
		for account, want := range map[*platform.Account]int{&platform.RegistryNodesUser: 200, nil: 401} {
			status, err := p.ManifestStatus(t.Context(), account, "e2e/build-app", first.Status.ImageDigest)
			if err != nil {
				t.Fatal(err)
			}
			if status != want {
				t.Errorf("manifest status as %v = %d, want %d", account, status, want)
			}
		}
	})

	t.Run("the built image runs", func(t *testing.T) {
		runBuiltImage(t, appRepository+"@"+first.Status.ImageDigest)
	})

	t.Run("a rebuild restores the cache", func(t *testing.T) {
		second := applyBuild(
			t,
			"e2e-app-2",
			p.UploadSource(t, "e2e-app-2", fixture),
			buildAppDockerfile,
			appRepository,
			appRepository+"@"+first.Status.CacheDigest,
		)
		if second.Status.Phase != succeeded {
			t.Fatalf("cached build phase %q (%s)", second.Status.Phase, second.Status.Message)
		}
		if logs := buildLogs(t, "e2e-app-2", "fetch"); !strings.Contains(logs, "cache restored") {
			t.Errorf("the fetch step did not restore the cache by digest:\n%s", logs)
		}
		if logs := buildLogs(t, "e2e-app-2", buildStep); !strings.Contains(logs, "CACHED") {
			t.Errorf("the build step reused no cached layer:\n%s", logs)
		}
		if !digestPattern.MatchString(second.Status.ImageDigest) {
			t.Errorf("cached build image digest %q", second.Status.ImageDigest)
		}
		t.Logf("cached build took %s (first build: %s)", buildDuration(second), buildDuration(first))
	})

	t.Run("an untrusted build finds no credentials", func(t *testing.T) {
		probeSource := t.TempDir()
		p.CopyFixture(t, "build-probe", probeSource)
		probe := applyBuild(
			t,
			"e2e-probe",
			p.UploadSource(t, "e2e-probe", probeSource),
			"Dockerfile",
			p.RegistryHost+"/e2e/build-probe",
			"",
		)
		if probe.Status.Phase != succeeded {
			t.Fatalf("probe build phase %q (%s)", probe.Status.Phase, probe.Status.Message)
		}
		output := buildOutput(t, "e2e-probe")
		if !strings.Contains(output, "PROBE-DONE") {
			t.Errorf("the probe did not run to completion inside the build:\n%s", output)
		}
		if strings.Contains(output, "PROBE-LEAK") {
			t.Errorf("the probe found credentials, tokens or Kubernetes API access:\n%s", output)
		}
	})

	t.Run("build egress is limited to its ports", func(t *testing.T) {
		registryHost, registryPort, _ := strings.Cut(p.RegistryHost, ":")
		addresses := p.MustKubectl(
			t,
			"get",
			"nodes",
			"-o",
			`jsonpath={.items[0].status.addresses[?(@.type=="InternalIP")].address}`,
		)
		nodeIP := strings.Fields(addresses)[0]
		for _, check := range []struct {
			namespace, host, port string
			want                  bool
		}{
			{"default", registryHost, registryPort, true},
			{"default", nodeIP, apiServerPort, true},
			{p.BuildNamespace, registryHost, registryPort, true},
			{p.BuildNamespace, nodeIP, apiServerPort, false},
		} {
			if got := probeTCP(t, check.namespace, check.host, check.port); got != check.want {
				t.Errorf(
					"a pod in %s reaching %s:%s = %v, want %v",
					check.namespace,
					check.host,
					check.port,
					got,
					check.want,
				)
			}
		}
	})
}

// The fake client withholds readiness by probe count so scheduler delays cannot change the result.
const buildkitStartupProbe = `set -eu
export PROBE_DIR=$(mktemp -d)
export BUILDCTL="$PROBE_DIR/client" BUILDKITD="$PROBE_DIR/daemon" ROOTLESSKIT=env
cat > "$BUILDCTL" <<'EOF'
#!/bin/sh
set -eu
case "$2" in
    debug)
        [ "${CRASH:-}" != 1 ] || exit 1
        attempts=$(cat "$PROBE_DIR/attempts")
        echo $((attempts + 1)) > "$PROBE_DIR/attempts"
        [ "$attempts" -ge 12 ]
        ;;
    build)
        [ "${CRASH:-}" != 1 ]
        [ "$(cat "$PROBE_DIR/attempts")" -gt 12 ]
        echo 'build started after delayed readiness'
        ;;
    *) exit 2 ;;
esac
EOF
cat > "$BUILDKITD" <<'EOF'
#!/bin/sh
if [ "${CRASH:-}" = 1 ]; then
    echo 'simulated buildkitd startup failure' >&2
    exit 1
fi
exec sleep 60
EOF
chmod +x "$BUILDCTL" "$BUILDKITD"
echo 0 > "$PROBE_DIR/attempts"
"$1" build
CRASH=1 exec "$1" build
`

func testBuildkitStartup(t *testing.T, jobName string) {
	t.Helper()
	var job batchv1.Job
	p.MustGet(t, &job, "-n", p.BuildNamespace, "job", jobName)
	spec := job.Spec.Template.Spec
	buildkit := spec.InitContainers[1]
	launcher := buildkit.Command[0]
	buildkit.Command = []string{"sh", "-c", buildkitStartupProbe, "startup-probe", launcher}
	buildkit.Args = nil
	buildkit.VolumeMounts = nil
	spec.InitContainers = nil
	spec.Containers = []corev1.Container{buildkit}
	spec.Volumes = nil
	pod := corev1.Pod{Spec: spec}
	pod.APIVersion, pod.Kind = "v1", "Pod"
	pod.Name, pod.Namespace = "buildkit-startup-probe", p.BuildNamespace
	manifest, err := json.Marshal(&pod)
	if err != nil {
		t.Fatal(err)
	}
	p.MustApply(t, string(manifest))
	p.DeleteOnCleanup(t, "-n", pod.Namespace, "pod", pod.Name)
	t.Cleanup(func() {
		if t.Failed() {
			p.LogKubectl(t, "-n "+pod.Namespace+" describe pod "+pod.Name, "-n "+pod.Namespace+" logs "+pod.Name)
		}
	})
	var observed corev1.Pod
	platform.Eventually(t, 2*time.Minute, "BuildKit startup probe to terminate", func(ctx context.Context) error {
		if err := p.Get(ctx, &observed, "-n", pod.Namespace, "pod", pod.Name); err != nil {
			return err
		}
		if observed.Status.Phase != corev1.PodFailed && observed.Status.Phase != corev1.PodSucceeded {
			return fmt.Errorf("pod phase %s", observed.Status.Phase)
		}
		return nil
	})
	logs := p.MustKubectl(t, "-n", pod.Namespace, "logs", pod.Name)
	if !strings.Contains(logs, "build started after delayed readiness") {
		t.Fatalf("launcher did not wait for the delayed daemon:\n%s", logs)
	}
	if observed.Status.Phase != corev1.PodFailed {
		t.Errorf("pod phase %s after daemon crash, want Failed", observed.Status.Phase)
	}
	if len(observed.Status.ContainerStatuses) != 1 {
		t.Fatalf("startup probe expected one container status, got %v", observed.Status.ContainerStatuses)
	}
	state := observed.Status.ContainerStatuses[0].State.Terminated
	if state == nil || state.ExitCode != 1 || !strings.Contains(state.Message, "simulated buildkitd startup failure") {
		t.Errorf("launcher did not preserve the daemon failure in the termination message: %v\n%s", state, logs)
	}
}

// applyBuild applies a Build in the build namespace and waits for it to finish.
func applyBuild(t *testing.T, name, sourceURL, dockerfile, repository, cacheRef string) *v1alpha1.Build {
	t.Helper()
	cache := ""
	if cacheRef != "" {
		cache = "\n  cacheRef: " + strconv.Quote(cacheRef)
	}
	p.MustApply(t, fmt.Sprintf(`apiVersion: infra.loco.io/v1alpha1
kind: Build
metadata:
  name: %s
  namespace: %s
spec:
  buildId: %s
  workspaceId: e2e-workspace
  platform: %s
  resourceId: e2e-resource
  sourceURL: %q
  dockerfilePath: %s
  context: .
  imageRepository: %s%s
`, name, p.BuildNamespace, name, p.TargetPlatform, sourceURL, dockerfile, repository, cache))
	var build v1alpha1.Build
	platform.Eventually(t, buildTimeout, "build "+name+" to finish", func(ctx context.Context) error {
		if err := p.Get(ctx, &build, "-n", p.BuildNamespace, "build", name); err != nil {
			return err
		}
		if !build.Finished() {
			return fmt.Errorf("phase %q", build.Status.Phase)
		}
		return nil
	})
	if build.Status.Phase != succeeded {
		dumpBuild(t, name)
	}
	return &build
}

func buildDuration(build *v1alpha1.Build) string {
	if build.Status.StartedAt == nil || build.Status.FinishedAt == nil {
		return "?"
	}
	return build.Status.FinishedAt.Sub(build.Status.StartedAt.Time).String()
}

func buildLogs(t *testing.T, job, container string) string {
	t.Helper()
	return p.MustKubectl(t, "-n", p.BuildNamespace, "logs", "job/"+job, "-c", container)
}

// buildOutput is the output of the build step's RUN instructions.
func buildOutput(t *testing.T, job string) string {
	t.Helper()
	step := regexp.MustCompile(`^#[0-9]+ [0-9.]+ `)
	var lines []string
	for line := range strings.Lines(buildLogs(t, job, buildStep)) {
		if step.MatchString(line) {
			lines = append(lines, step.ReplaceAllString(line, ""))
		}
	}
	return strings.Join(lines, "")
}

func dumpBuild(t *testing.T, name string) {
	t.Helper()
	builds := "-n " + p.BuildNamespace + " "
	p.LogKubectl(t,
		builds+"get build "+name+" -o=jsonpath={.status}",
		builds+"get job "+name+" -o wide",
		builds+"get pods -l batch.kubernetes.io/job-name="+name+
			" -o=custom-columns=NAME:.metadata.name,NODE:.spec.nodeName,PHASE:.status.phase,"+
			"INIT:.status.initContainerStatuses[*].state,CONTAINERS:.status.containerStatuses[*].state",
		builds+"logs job/"+name+" -c fetch --tail 100",
		builds+"logs job/"+name+" -c build --tail 100",
		builds+"logs job/"+name+" -c push --tail 100",
		builds+"get events --sort-by=.lastTimestamp",
		"-n "+p.Namespace+" logs deployment/loco-build-controller --tail 100",
	)
}

func mounts(containers []corev1.Container, container, volume string) bool {
	for _, c := range containers {
		if c.Name != container {
			continue
		}
		for _, mount := range c.VolumeMounts {
			if mount.Name == volume {
				return true
			}
		}
	}
	return false
}

// runBuiltImage runs image as a Deployment outside Loco, pulling with the nodes account's Secret.
func runBuiltImage(t *testing.T, image string) {
	t.Helper()
	const namespace = "e2e-build-run"
	p.MustApply(t, "apiVersion: v1\nkind: Namespace\nmetadata:\n  name: "+namespace+"\n")
	auth := p.MustKubectl(
		t,
		"-n",
		p.Namespace,
		"get",
		"secret",
		"loco-registry",
		"-o",
		`jsonpath={.data.\.dockerconfigjson}`,
	)
	p.MustApply(t, fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: registry-pull
  namespace: %s
type: kubernetes.io/dockerconfigjson
data:
  .dockerconfigjson: %s
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: build-app
  namespace: %s
spec:
  replicas: 1
  selector:
    matchLabels: {app: build-app}
  template:
    metadata:
      labels: {app: build-app}
    spec:
      imagePullSecrets: [{name: registry-pull}]
      containers:
        - name: app
          image: %s
          ports: [{containerPort: 8080}]
          readinessProbe:
            httpGet: {path: /healthz, port: 8080}
            periodSeconds: 2
`, namespace, auth, namespace, image))
	platform.Eventually(
		t,
		3*time.Minute,
		"the built image to run and pass its readiness probe",
		func(ctx context.Context) error {
			var deployment appsv1.Deployment
			if err := p.Get(ctx, &deployment, "-n", namespace, "deployment", "build-app"); err != nil {
				return err
			}
			if deployment.Status.ReadyReplicas < 1 {
				return fmt.Errorf("%d ready replicas", deployment.Status.ReadyReplicas)
			}
			return nil
		},
	)
}

// probeTCP reports whether a pod in namespace connects to host:port, once the CNI enforces the
// network policy on a pod in the build namespace.
func probeTCP(t *testing.T, namespace, host, port string) bool {
	t.Helper()
	name := fmt.Sprintf("tcp-probe-%d", time.Now().UnixNano()%100000)
	p.StartPod(t, platform.ProbePod{Namespace: namespace, Name: name, Command: []string{"sleep", "60"}})
	p.DeleteOnCleanup(t, "-n", namespace, "pod", name)
	p.MustKubectl(t, "-n", namespace, "wait", "--for=condition=Ready", "pod/"+name, "--timeout=60s")
	if namespace == p.BuildNamespace {
		ip := strings.TrimSpace(p.MustKubectl(t, "-n", namespace, "get", "pod", name, "-o", "jsonpath={.status.podIP}"))
		platform.Eventually(t, time.Minute, "the CNI to enforce policy on "+name, func(ctx context.Context) error {
			set, err := p.Command(
				ctx,
				"docker",
				append([]string{"exec", p.Node(), "nft", "list", "set"}, strings.Fields(policySet)...)...)
			if err != nil {
				return err
			}
			if !regexp.MustCompile(`\b` + regexp.QuoteMeta(ip) + `\b`).MatchString(set) {
				return fmt.Errorf("%s is not in %s", ip, policySet)
			}
			return nil
		})
	}
	_, err := p.Kubectl(t.Context(), "-n", namespace, "exec", name, "--", "nc", "-z", "-w", "3", host, port)
	return err == nil
}
