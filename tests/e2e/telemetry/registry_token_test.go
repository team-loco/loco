package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"

	buildv1 "github.com/team-loco/loco/gen/go/loco/build/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	"github.com/team-loco/loco/internal/locofile"
	"github.com/team-loco/loco/tests/e2e/platform"
)

const (
	pullUser          = "loco-pull"
	buildUser         = "loco-build"
	issuedAt          = "loco.io/registry-pull-issued-at"
	expiresAt         = "loco.io/registry-pull-expires-at"
	buildController   = "loco-build-controller"
	deployTimeout     = 3 * time.Minute
	pullTimeout       = 2 * time.Minute
	renewalTimeout    = 150 * time.Second
	sweepTimeout      = 90 * time.Second
	secretTimeout     = time.Minute
	revocationTimeout = 90 * time.Second
	buildTimeout      = 10 * time.Minute
	controllerTimeout = time.Minute
	expiryMargin      = 10 * time.Second
)

var digestPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

// TestRegistryToken runs the registry in token mode: the API as its token endpoint, per-workspace
// pull credentials and per-build credentials the agent writes, their scopes, renewal and
// revocation, and the image sweeper on its own tokens.
func TestRegistryToken(t *testing.T) {
	t.Parallel()
	a := p.NewTenant(t)
	b := a.NewWorkspace(t, a.Name+"-b")
	r := &registryRun{a: a, b: b, firstBuild: map[*platform.Workspace]string{}}

	t.Run("the registry challenges with the named realm", namedRealm)

	t.Run("first deploys in new workspaces", r.firstDeploys)

	t.Run("no shared secrets", r.noSharedSecrets)

	r.repoA, r.repoB = repository(t, r.firstBuild[a]), repository(t, r.firstBuild[b])
	r.digestA, r.digestB = imageDigest(t, r.firstBuild[a]), imageDigest(t, r.firstBuild[b])

	t.Run("a pull credential is scoped to its workspace", r.pullCredentialScoped)

	t.Run("the static shared passwords are refused", r.staticPasswordsRefused)

	t.Run("a pod cannot pull another workspace's image", r.crossWorkspacePull)

	t.Run("a second deploy imports the cache", r.secondDeploy)

	t.Run("the sweeper deletes the replaced image", r.sweeper)

	t.Run("a resent build gets a new credential", r.resentBuild)

	t.Run("a build credential is scoped to its repository", r.buildCredentialScoped)

	t.Run("the resent build succeeds", r.resentSucceeds)

	t.Run("a finished build's credential is refused", r.finishedCredential)

	t.Run("renewal replaces the pull Secret", r.renewal)

	t.Run("a removed workspace loses its credential", r.removedWorkspace)
}

const (
	buildSucceeded  = "succeeded"
	repositoryScope = "repository:"
	// repositoryAccess is the type of a registry token's access entry for a repository.
	repositoryAccess = "repository"
	pullAction       = "pull"
	appA             = "rt-app-a"
	appB             = "rt-app-b"
)

// registryRun is what TestRegistryToken's steps build on: the two workspaces, their builds and
// images, and the credentials of the resent build.
type registryRun struct {
	a, b           *platform.Workspace
	firstBuild     map[*platform.Workspace]string
	repoA, repoB   string
	digestA        string
	digestB        string
	secondA        string
	resent         string
	firstPassword  string
	secondPassword string
}

// deploySource deploys the build-app fixture, changed per label, as a private service.
func deploySource(t *testing.T, w *platform.Workspace, app, label string) {
	t.Helper()
	dir := t.TempDir()
	writeSource(t, dir, label)
	platform.WriteLocoFile(
		t,
		dir,
		locofile.File{Partial: app, Services: map[string]locofile.Service{app: platform.SourceService()}},
	)
	w.MustLoco(t, dir, "deploy", "--yes", "--wait")
}

func writeSource(t *testing.T, dir, label string) {
	t.Helper()
	p.CopyFixture(t, "build-app", dir)
	file, err := os.OpenFile(filepath.Join(dir, "main.go"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := fmt.Fprintf(file, "\nfunc init() { log.SetPrefix(%q) }\n", label+" "); err != nil {
		t.Fatal(err)
	}
}

// startBuild creates, uploads and starts a build of the app through the API.
func startBuild(t *testing.T, w *platform.Workspace, app, label string) string {
	t.Helper()
	source := t.TempDir()
	writeSource(t, source, label)
	archive := p.PackSource(t, source)
	info, err := os.Stat(archive)
	if err != nil {
		t.Fatal(err)
	}
	created, err := p.User.Build.CreateBuild(t.Context(), &buildv1.CreateBuildRequest{
		ResourceId: w.ResourceID(t, app), DockerfilePath: "deploy/Dockerfile", SourceSize: info.Size(),
	})
	if err != nil {
		t.Fatal(err)
	}
	platform.Upload(t, created.GetUploadUrl(), archive)
	if _, err := p.User.Build.StartBuild(
		t.Context(),
		&buildv1.StartBuildRequest{BuildId: created.GetBuildId()},
	); err != nil {
		t.Fatal(err)
	}
	return created.GetBuildId()
}

func latestBuild(t *testing.T, w *platform.Workspace, app string) string {
	t.Helper()
	var id string
	if err := p.DB.QueryRow(t.Context(), `SELECT b.id FROM builds b JOIN resources r ON r.id = b.resource_id
		WHERE r.workspace_id = $1 AND r.name = $2 ORDER BY b.created_at DESC LIMIT 1`, w.ID, app).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func buildStatus(t *testing.T, id string) string {
	t.Helper()
	var status string
	if err := p.DB.QueryRow(t.Context(), "SELECT status FROM builds WHERE id = $1", id).Scan(&status); err != nil {
		t.Fatal(err)
	}
	return status
}

// repository is a build's image repository without the registry host.
func repository(t *testing.T, build string) string {
	t.Helper()
	var image string
	if err := p.DB.QueryRow(t.Context(), "SELECT image_repository FROM builds WHERE id = $1", build).
		Scan(&image); err != nil {
		t.Fatal(err)
	}
	repository, ok := strings.CutPrefix(image, p.RegistryHost+"/")
	if !ok || repository == "" {
		t.Fatalf("build %s pushed to %s, not into %s", build, image, p.RegistryHost)
	}
	return repository
}

func imageDigest(t *testing.T, build string) string {
	t.Helper()
	var digest string
	if err := p.DB.QueryRow(t.Context(), "SELECT image_digest FROM builds WHERE id = $1", build).
		Scan(&digest); err != nil {
		t.Fatal(err)
	}
	if !digestPattern.MatchString(digest) {
		t.Fatalf("build %s image digest %q", build, digest)
	}
	return digest
}

func pullSecret(w *platform.Workspace) string {
	return "loco-registry-pull-" + w.ID
}

func pullPassword(t *testing.T, w *platform.Workspace) string {
	t.Helper()
	credential, err := p.RegistrySecret(t.Context(), p.Namespace, pullSecret(w))
	if err != nil {
		t.Fatal(err)
	}
	return credential.Password
}

func annotation(t *testing.T, w *platform.Workspace, key string) string {
	t.Helper()
	var secret corev1.Secret
	p.MustGet(t, &secret, "-n", p.Namespace, "secret", pullSecret(w))
	value := secret.Annotations[key]
	if value == "" {
		t.Fatalf("%s has no %s annotation", pullSecret(w), key)
	}
	return value
}

func secondsLeft(t *testing.T, expiry string) time.Duration {
	t.Helper()
	at, err := time.Parse(time.RFC3339, expiry)
	if err != nil {
		t.Fatal(err)
	}
	return time.Until(at)
}

func logLine(t *testing.T, substring string) int {
	t.Helper()
	number, err := p.LogLineNumber("agent", substring)
	if err != nil {
		t.Fatal(err)
	}
	return number
}

func realmToken(t *testing.T, user, password string, scopes ...string) string {
	t.Helper()
	response, err := p.RealmToken(t.Context(), user, password, scopes...)
	if err != nil {
		t.Fatal(err)
	}
	if response.Status != http.StatusOK || response.Token == "" {
		t.Fatalf("the token endpoint answered %s for %v with HTTP %d", user, scopes, response.Status)
	}
	return response.Token
}

func requireRealmStatus(t *testing.T, user, password, scope string, want int) {
	t.Helper()
	response, err := p.RealmToken(t.Context(), user, password, scope)
	if err != nil {
		t.Fatal(err)
	}
	if response.Status != want {
		t.Errorf("the token endpoint answered %s for %s with HTTP %d, want %d", user, scope, response.Status, want)
	}
}

func requireAccess(t *testing.T, token string, want ...platform.Access) {
	t.Helper()
	access, err := platform.TokenAccess(token)
	if err != nil {
		t.Fatal(err)
	}
	if len(access) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(access, want) {
		t.Errorf("the token grants %+v, want %+v", access, want)
	}
}

func registryStatus(t *testing.T, path, token string) int {
	t.Helper()
	response, err := p.RegistryRequest(t.Context(), http.MethodGet, path, nil, token)
	if err != nil {
		t.Fatal(err)
	}
	return response.Status
}

// requireRefusal checks the registry answers a request with 401 and a challenge for action on
// repository.
func requireRefusal(t *testing.T, method, path, token string, account *platform.Account, repository, action string) {
	t.Helper()
	response, err := p.RegistryRequest(t.Context(), method, path, account, token)
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf(
		`Bearer realm=%q,service=%q,scope="repository:%s:%s"`,
		p.RegistryRealm,
		p.RegistryService,
		repository,
		action,
	)
	if response.Status != http.StatusUnauthorized || response.Challenge() != want {
		t.Errorf("%s %s: HTTP %d with %q, want 401 with %q", method, path, response.Status, response.Challenge(), want)
	}
}

func pulledManifest(t *testing.T, w *platform.Workspace, build string) int {
	t.Helper()
	repository := repository(t, build)
	return registryStatus(t, "/v2/"+repository+"/manifests/"+imageDigest(t, build),
		realmToken(t, pullUser, pullPassword(t, w), repositoryScope+repository+":pull"))
}

// startPullPod runs image in namespace with the workspace's pull Secret, pulling on every start.
func startPullPod(t *testing.T, namespace, name, image string) {
	t.Helper()
	p.MustKubectl(t, "-n", namespace, "delete", "pod", name, "--ignore-not-found", "--wait")
	p.MustApply(t, fmt.Sprintf(`apiVersion: v1
kind: Pod
metadata:
  name: %s
  namespace: %s
spec:
  automountServiceAccountToken: false
  terminationGracePeriodSeconds: 0
  imagePullSecrets: [{name: loco-registry-pull}]
  securityContext:
    runAsNonRoot: true
    runAsUser: 65534
    seccompProfile: {type: RuntimeDefault}
  containers:
    - name: app
      image: %s
      imagePullPolicy: Always
      resources:
        requests: {cpu: 10m, memory: 32Mi}
        limits: {cpu: 100m, memory: 64Mi}
      securityContext:
        allowPrivilegeEscalation: false
        capabilities: {drop: ["ALL"]}
`, name, namespace, image))
	p.DeleteOnCleanup(t, "-n", namespace, "pod", name)
}

func waitingReason(ctx context.Context, t *testing.T, namespace, name string) string {
	t.Helper()
	var pod corev1.Pod
	if err := p.Get(ctx, &pod, "-n", namespace, "pod", name); err != nil || len(pod.Status.ContainerStatuses) == 0 ||
		pod.Status.ContainerStatuses[0].State.Waiting == nil {
		return ""
	}
	return pod.Status.ContainerStatuses[0].State.Waiting.Reason
}

func namedRealm(t *testing.T) {
	response, err := p.RegistryRequest(t.Context(), http.MethodGet, "/v2/", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf(`Bearer realm=%q,service=%q,scope=""`, p.RegistryRealm, p.RegistryService)
	if response.Status != http.StatusUnauthorized || response.Challenge() != want {
		t.Errorf("anonymous /v2/: HTTP %d with %q, want 401 with %q", response.Status, response.Challenge(), want)
	}
	realm, err := url.Parse(p.RegistryRealm)
	if err != nil {
		t.Fatal(err)
	}
	if net.ParseIP(realm.Hostname()) != nil {
		t.Errorf("the realm host %s is an IP literal, not a DNS name", realm.Hostname())
	}
}

func (r *registryRun) firstDeploys(t *testing.T) {
	a, b, firstBuild := r.a, r.b, r.firstBuild
	for w, app := range map[*platform.Workspace]string{a: appA, b: appB} {
		deploySource(t, w, app, app+"-1")
		firstBuild[w] = latestBuild(t, w, app)
	}
	for w, app := range map[*platform.Workspace]string{a: appA, b: appB} {
		platform.Eventually(
			t,
			deployTimeout,
			app+"'s pods to pull the built image and run",
			func(ctx context.Context) error {
				var pods corev1.PodList
				if err := p.Get(ctx, &pods, "-n", w.Namespace, "pods"); err != nil {
					return err
				}
				for _, pod := range pods.Items {
					if pod.Status.Phase == corev1.PodRunning {
						return nil
					}
				}
				return fmt.Errorf("no running pod in %s", w.Namespace)
			},
		)
		if status := buildStatus(t, firstBuild[w]); status != buildSucceeded {
			t.Errorf("build of %s is %s", app, status)
		}
		secretLine := logLine(t, `"msg":"wrote registry pull Secret","name":"`+pullSecret(w)+`"`)
		applied := logLine(t, `"msg":"applied Application","name":"resource-`+w.ResourceID(t, app)+`"`)
		if secretLine == 0 || applied == 0 || secretLine > applied {
			t.Errorf(
				"the agent wrote %s at log line %d and applied %s's first Application at line %d, want the Secret first",
				pullSecret(w),
				secretLine,
				app,
				applied,
			)
		}
	}
}

func (r *registryRun) noSharedSecrets(t *testing.T) {
	a, b := r.a, r.b
	for _, secret := range [][2]string{{p.Namespace, "loco-registry"}, {p.Namespace, "loco-registry-push"},
		{p.BuildNamespace, "registry-push"}, {p.BuildNamespace, "registry-pull"}} {
		if found, err := p.Exists(t.Context(), "-n", secret[0], "secret", secret[1]); err != nil || found {
			t.Errorf("Secret %s/%s exists (%v)", secret[0], secret[1], err)
		}
	}
	for _, w := range []*platform.Workspace{a, b} {
		copied, err := p.RegistrySecret(t.Context(), w.Namespace, "loco-registry-pull")
		if err != nil {
			t.Fatal(err)
		}
		if copied.Username != pullUser {
			t.Errorf("workspace %s's copy is a %q credential, want %s", w.ID, copied.Username, pullUser)
		}
		if copied.Password != pullPassword(t, w) {
			t.Errorf("workspace %s's copy does not hold the agent's credential", w.ID)
		}
	}
}

func (r *registryRun) pullCredentialScoped(t *testing.T) {
	a, b, repoA, repoB, digestA, digestB := r.a, r.b, r.repoA, r.repoB, r.digestA, r.digestB
	password := pullPassword(t, a)
	token := realmToken(t, pullUser, password, repositoryScope+repoA+":pull")
	requireAccess(t, token, platform.Access{Type: repositoryAccess, Name: repoA, Actions: []string{pullAction}})
	if status := registryStatus(t, "/v2/"+repoA+"/manifests/"+digestA,
		token,
	); status != http.StatusOK {
		t.Errorf("A's image with A's token: HTTP %d", status)
	}
	resourceA, resourceB := repoA[strings.LastIndex(repoA, "/")+1:], repoB[strings.LastIndex(repoB, "/")+1:]
	for _, scope := range []string{
		repoB,
		"ws-" + a.ID + "x/" + resourceA,
		"ws-" + a.ID + "/../ws-" + b.ID + "/" + resourceB,
		"other/ws-" + a.ID + "/" + resourceA,
	} {
		requireAccess(t, realmToken(t, pullUser, password, repositoryScope+scope+":pull"))
	}
	requireRefusal(
		t,
		http.MethodGet,
		"/v2/"+repoB+"/manifests/"+digestB,
		realmToken(t, pullUser, password, repositoryScope+repoB+":pull"),
		nil,
		repoB,
		"pull",
	)
	token = realmToken(t, pullUser, password, repositoryScope+repoA+":pull,push")
	requireAccess(t, token, platform.Access{Type: repositoryAccess, Name: repoA, Actions: []string{pullAction}})
	requireRefusal(t, http.MethodPost, "/v2/"+repoA+"/blobs/uploads/", token, nil, repoA, "push")
	for _, login := range [][2]string{{pullUser, token}, {buildUser, token}, {buildUser, password}} {
		requireRealmStatus(t, login[0], login[1], repositoryScope+repoA+":pull", http.StatusUnauthorized)
	}
}

func (r *registryRun) staticPasswordsRefused(t *testing.T) {
	repoA, digestA := r.repoA, r.digestA
	accounts := []platform.Account{platform.RegistryNodesUser, platform.RegistryBuilderUser, platform.RegistryAPIUser}
	for _, account := range accounts {
		requireRefusal(t, http.MethodGet, "/v2/"+repoA+"/manifests/"+digestA, "", &account, repoA, "pull")
		requireRefusal(t, http.MethodPost, "/v2/"+repoA+"/blobs/uploads/", "", &account, repoA, "push")
	}
}

func (r *registryRun) crossWorkspacePull(t *testing.T) {
	a, repoB, digestB := r.a, r.repoB, r.digestB
	const pod = "rt-cross-pull"
	startPullPod(t, a.Namespace, pod, p.RegistryHost+"/"+repoB+"@"+digestB)
	platform.Eventually(
		t,
		pullTimeout,
		"the pull of B's image in A's namespace to fail",
		func(ctx context.Context) error {
			if reason := waitingReason(
				ctx,
				t,
				a.Namespace,
				pod,
			); reason != "ErrImagePull" &&
				reason != "ImagePullBackOff" {
				return fmt.Errorf("waiting reason %q", reason)
			}
			return nil
		},
	)
	var events corev1.EventList
	p.MustGet(
		t,
		&events,
		"-n",
		a.Namespace,
		"events",
		"--field-selector",
		"involvedObject.name="+pod+",reason=Failed",
	)
	refused := regexp.MustCompile(`unexpected status from HEAD request to [^ ]*/manifests/[^ ]*: 401 Unauthorized`)
	found := false
	for _, event := range events.Items {
		found = found || refused.MatchString(event.Message)
	}
	if !found {
		t.Errorf("the registry did not refuse the pull with 401 Unauthorized: %v", events.Items)
	}
}

func (r *registryRun) secondDeploy(t *testing.T) {
	a, firstBuild := r.a, r.firstBuild
	deploySource(t, a, appA, appA+"-2")
	r.secondA = latestBuild(t, a, appA)
	if r.secondA == firstBuild[a] {
		t.Fatal("the second deploy made no new build")
	}
	if logs := p.MustKubectl(
		t,
		"-n",
		p.BuildNamespace,
		"logs",
		"job/build-"+r.secondA,
		"-c",
		"fetch",
	); !strings.Contains(
		logs,
		"cache restored",
	) {
		t.Errorf("the second build's fetch step did not import the cache:\n%s", logs)
	}
}

func (r *registryRun) sweeper(t *testing.T) {
	a, firstBuild, secondA := r.a, r.firstBuild, r.secondA
	platform.Eventually(
		t,
		sweepTimeout,
		"the image sweeper to delete the replaced image",
		func(context.Context) error {
			if status := pulledManifest(t, a, firstBuild[a]); status != http.StatusNotFound {
				return fmt.Errorf("manifest status %d", status)
			}
			return nil
		},
	)
	var deleted bool
	if err := p.DB.QueryRow(t.Context(), "SELECT image_deleted_at IS NOT NULL FROM builds WHERE id = $1", firstBuild[a]).
		Scan(&deleted); err != nil {
		t.Fatal(err)
	}
	if !deleted {
		t.Error("the API did not record the image deletion")
	}
	if status := pulledManifest(t, a, secondA); status != http.StatusOK {
		t.Errorf("the deployed build's image: HTTP %d", status)
	}
}

func (r *registryRun) resentBuild(t *testing.T) {
	a := r.a
	p.MustKubectl(t, "-n", p.Namespace, "scale", "deployment", buildController, "--replicas=0")
	platform.Eventually(t, controllerTimeout, "the build controller to stop", func(ctx context.Context) error {
		var pods corev1.PodList
		if err := p.Get(
			ctx,
			&pods,
			"-n",
			p.Namespace,
			"pods",
			"-l",
			"app.kubernetes.io/name="+buildController,
		); err != nil {
			return err
		}
		if len(pods.Items) != 0 {
			return fmt.Errorf("%d controller pods", len(pods.Items))
		}
		return nil
	})
	r.resent = startBuild(t, a, appA, appA+"-resend")
	platform.Eventually(
		t,
		secretTimeout,
		"the agent to write the build's registry Secret",
		func(ctx context.Context) error {
			credential, err := p.RegistrySecret(ctx, p.BuildNamespace, "build-"+r.resent+"-registry")
			r.firstPassword = credential.Password
			return err
		},
	)
	credential, err := p.RegistrySecret(t.Context(), p.BuildNamespace, "build-"+r.resent+"-registry")
	if err != nil {
		t.Fatal(err)
	}
	if credential.Username != buildUser {
		t.Errorf("the build Secret is a %q credential, want %s", credential.Username, buildUser)
	}
	if err := p.RestartAgent(t.Context()); err != nil {
		t.Fatal(err)
	}
	platform.Eventually(
		t,
		secretTimeout,
		"the r.resent StartBuild to replace the build Secret's password",
		func(ctx context.Context) error {
			credential, err := p.RegistrySecret(ctx, p.BuildNamespace, "build-"+r.resent+"-registry")
			if err != nil {
				return err
			}
			if credential.Password == r.firstPassword {
				return errors.New("the password is unchanged")
			}
			r.secondPassword = credential.Password
			return nil
		},
	)
	if status := buildStatus(t, r.resent); status != "queued" {
		t.Errorf("the r.resent build is %s, want queued", status)
	}
}

func (r *registryRun) buildCredentialScoped(t *testing.T) {
	b, repoA, repoB, digestB, secondPassword := r.b, r.repoA, r.repoB, r.digestB, r.secondPassword
	token := realmToken(t, buildUser, secondPassword, repositoryScope+repoA+":pull,push")
	requireAccess(t, token, platform.Access{Type: repositoryAccess, Name: repoA, Actions: []string{"pull", "push"}})
	token = realmToken(t, buildUser, secondPassword, repositoryScope+repoB+":pull,push")
	requireAccess(t, token)
	requireRefusal(t, http.MethodPost, "/v2/"+repoB+"/blobs/uploads/", token, nil, repoB, "push")
	manifest, err := p.RegistryRequest(t.Context(), http.MethodGet, "/v2/"+repoB+"/manifests/"+digestB, nil,
		realmToken(t, pullUser, pullPassword(t, b), repositoryScope+repoB+":pull"))
	if err != nil {
		t.Fatal(err)
	}
	var image struct {
		Layers []struct {
			Digest string `json:"digest"`
		} `json:"layers"`
	}
	if err = json.Unmarshal(
		manifest.Body,
		&image,
	); err != nil || len(image.Layers) == 0 ||
		!digestPattern.MatchString(image.Layers[len(image.Layers)-1].Digest) {
		t.Fatalf("read the top layer of B's image: %v %s", err, manifest.Body)
	}
	layer := image.Layers[len(image.Layers)-1].Digest
	token = realmToken(t, buildUser, secondPassword, repositoryScope+repoA+":pull,push", repositoryScope+repoB+":pull")
	requireAccess(t, token, platform.Access{Type: repositoryAccess, Name: repoA, Actions: []string{"pull", "push"}})
	if status := registryStatus(t, "/v2/"+repoA+"/blobs/"+layer,
		token,
	); status != http.StatusNotFound {
		t.Errorf("B's top layer in A's repository: HTTP %d, want 404", status)
	}
	mount, err := p.RegistryRequest(
		t.Context(),
		http.MethodPost,
		"/v2/"+repoA+"/blobs/uploads/?mount="+layer+"&from="+repoB,
		nil,
		token,
	)
	if err != nil {
		t.Fatal(err)
	}
	if mount.Status != http.StatusAccepted ||
		!regexp.MustCompile(`(?i)^/v2/`+regexp.QuoteMeta(repoA)+`/blobs/uploads/[^/?]+`).
			MatchString(mount.Header.Get("Location")) {
		t.Errorf(
			"a mount from B: HTTP %d at %q, want 202 and an ordinary upload in A",
			mount.Status,
			mount.Header.Get("Location"),
		)
	}
	if status := registryStatus(t, "/v2/"+repoA+"/blobs/"+layer,
		token,
	); status != http.StatusNotFound {
		t.Errorf("B's layer after the mount: HTTP %d, want 404", status)
	}
}

func (r *registryRun) resentSucceeds(t *testing.T) {
	resent := r.resent
	p.MustKubectl(t, "-n", p.Namespace, "scale", "deployment", buildController, "--replicas=1")
	platform.Eventually(t, buildTimeout, "the resent build to finish", func(context.Context) error {
		if status := buildStatus(t, resent); status != buildSucceeded && status != "failed" && status != "canceled" {
			return fmt.Errorf("build %s", status)
		}
		return nil
	})
	if status := buildStatus(t, resent); status != buildSucceeded {
		t.Errorf("the resent build ended %s", status)
	}
	var job batchv1.Job
	p.MustGet(t, &job, "-n", p.BuildNamespace, "job", "build-"+resent)
	mounted := false
	for _, volume := range job.Spec.Template.Spec.Volumes {
		mounted = mounted || volume.Secret != nil && volume.Secret.SecretName == "build-"+resent+"-registry"
	}
	if !mounted {
		t.Error("the build's Job does not mount its own registry Secret")
	}
}

func (r *registryRun) finishedCredential(t *testing.T) {
	firstPassword, secondPassword, repoA := r.firstPassword, r.secondPassword, r.repoA
	for _, password := range []string{firstPassword, secondPassword} {
		requireRealmStatus(t, buildUser, password, repositoryScope+repoA+":pull", http.StatusUnauthorized)
	}
}

func (r *registryRun) renewal(t *testing.T) {
	b, repoB, digestB := r.b, r.repoB, r.digestB
	before := pullPassword(t, b)
	issued, previousExpiry := annotation(t, b, issuedAt), annotation(t, b, expiresAt)
	var renewed string
	platform.Eventually(
		t,
		renewalTimeout,
		"the agent to renew B's pull credential (issued "+issued+")",
		func(context.Context) error {
			if renewed = pullPassword(t, b); renewed == before {
				return errors.New("the password is unchanged")
			}
			return nil
		},
	)
	if reissued := annotation(t, b, issuedAt); reissued <= issued {
		t.Errorf("the renewed issue time %s is not after %s", reissued, issued)
	}
	requireRealmStatus(t, pullUser, renewed, repositoryScope+repoB+":pull", http.StatusOK)
	left := secondsLeft(t, previousExpiry)
	switch {
	case left > expiryMargin:
		requireRealmStatus(t, pullUser, before, repositoryScope+repoB+":pull", http.StatusOK)
	case left < -expiryMargin:
		requireRealmStatus(t, pullUser, before, repositoryScope+repoB+":pull", http.StatusUnauthorized)
	default:
		t.Logf("the previous credential expires within %s (%s); not checking it", expiryMargin, left)
	}
	platform.Eventually(
		t,
		secretTimeout,
		"the controller to copy the renewed credential into B's namespace",
		func(ctx context.Context) error {
			copied, err := p.RegistrySecret(ctx, b.Namespace, "loco-registry-pull")
			if err == nil && copied.Password != renewed {
				return errors.New("B's namespace holds another credential")
			}
			return err
		},
	)
	const pod = "rt-renewed-pull"
	startPullPod(t, b.Namespace, pod, p.RegistryHost+"/"+repoB+"@"+digestB)
	platform.Eventually(
		t,
		pullTimeout,
		"a pod in B to pull its image with the renewed credential",
		func(ctx context.Context) error {
			var status corev1.Pod
			if err := p.Get(ctx, &status, "-n", b.Namespace, "pod", pod); err != nil {
				return err
			}
			if len(status.Status.ContainerStatuses) == 0 ||
				status.Status.ContainerStatuses[0].State.Running == nil {
				return errors.New("the pod has not started")
			}
			return nil
		},
	)
}

func (r *registryRun) removedWorkspace(t *testing.T) {
	a, b, secondA := r.a, r.b, r.secondA
	repository := repository(t, secondA)
	password := pullPassword(t, a)
	expiry := annotation(t, a, expiresAt)
	requireRealmStatus(t, pullUser, password, repositoryScope+repository+":pull", http.StatusOK)
	if _, err := p.User.Resource.DeleteResource(
		t.Context(),
		&resourcev1.DeleteResourceRequest{ResourceId: a.ResourceID(t, appA)},
	); err != nil {
		t.Fatal(err)
	}
	platform.Eventually(
		t,
		revocationTimeout,
		"A's credential to stop working at the token endpoint",
		func(ctx context.Context) error {
			response, err := p.RealmToken(ctx, pullUser, password, repositoryScope+repository+":pull")
			if err == nil && response.Status != http.StatusUnauthorized {
				return fmt.Errorf("HTTP %d", response.Status)
			}
			return err
		},
	)
	if left := secondsLeft(t, expiry); left <= expiryMargin {
		t.Errorf("the refusal came %s before the credential's expiry, want more than %s", left, expiryMargin)
	}
	platform.Eventually(
		t,
		revocationTimeout,
		"the agent to delete A's pull Secret",
		func(ctx context.Context) error {
			if found, err := p.Exists(ctx, "-n", p.Namespace, "secret", pullSecret(a)); err != nil || found {
				return fmt.Errorf("%s exists (%w)", pullSecret(a), err)
			}
			return nil
		},
	)
	pullPassword(t, b)
}
