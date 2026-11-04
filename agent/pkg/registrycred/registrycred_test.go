package registrycred

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strconv"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectinprocess"
	"google.golang.org/protobuf/types/known/timestamppb"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	registryv1 "github.com/team-loco/loco/gen/go/loco/registry/v1"
	"github.com/team-loco/loco/gen/go/loco/registry/v1/registryv1connect"
	locoControllerV1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

const (
	testNamespace     = "loco-system"
	testWorkspace     = "01a33333-3333-7333-8333-333333333333"
	testOtherSpace    = "01a55555-5555-7555-8555-555555555555"
	testHost          = "registry.loco.test:5000"
	testPullUser      = locoControllerV1.RegistryPullUsername
	testLifetime      = 24 * time.Hour
	testInterval      = 5 * time.Minute
	testRetryDelay    = 10 * time.Millisecond
	testWait          = 5 * time.Second
	testPoll          = 5 * time.Millisecond
	testOutage        = "control plane unavailable"
	testShortLifetime = 200 * time.Millisecond
	testSettle        = 100 * time.Millisecond
	testRenewals      = 3
	testFirstPassword = "secret-1"
)

type fakeIssuer struct {
	registryv1connect.UnimplementedRegistryServiceHandler
	mu         sync.Mutex
	now        func() time.Time
	failures   int
	code       connect.Code
	lifetime   time.Duration
	refused    map[string]bool
	calls      int
	workspaces []string
}

func (f *fakeIssuer) IssuePullCredential(
	_ context.Context,
	req *registryv1.IssuePullCredentialRequest,
) (*registryv1.IssuePullCredentialResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.calls <= f.failures {
		return nil, connect.NewError(f.code, testOutage)
	}
	if f.refused[req.GetWorkspaceId()] {
		return nil, connect.NewError(connect.CodePermissionDenied, "the workspace has no placement on this cluster")
	}
	f.workspaces = append(f.workspaces, req.GetWorkspaceId())
	lifetime := testLifetime
	if f.lifetime > 0 {
		lifetime = f.lifetime
	}
	expiresAt := f.now().Add(lifetime)
	return &registryv1.IssuePullCredentialResponse{
		Username:     testPullUser,
		Password:     "secret-" + strconv.Itoa(f.calls),
		ExpiresAt:    timestamppb.New(expiresAt),
		RegistryHost: testHost,
	}, nil
}

func (f *fakeIssuer) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func testBackoff() wait.Backoff {
	return wait.Backoff{Duration: testRetryDelay, Factor: 1, Steps: 1 << 30, Cap: testRetryDelay}
}

type fixture struct {
	manager *Manager
	client  client.Client
	issuer  *fakeIssuer
	clock   *clock
}

func newFixture(t *testing.T, interval time.Duration, existing ...client.Object) *fixture {
	t.Helper()
	return newInterceptedFixture(t, interval, interceptor.Funcs{}, existing...)
}

func newInterceptedFixture(
	t *testing.T,
	interval time.Duration,
	funcs interceptor.Funcs,
	existing ...client.Object,
) *fixture {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := locoControllerV1.AddToScheme(scheme); err != nil {
		t.Fatalf("add scheme: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("add core scheme: %v", err)
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(existing...).WithInterceptorFuncs(funcs).Build()
	clk := &clock{now: time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)}
	issuer := &fakeIssuer{now: clk.Now}
	server := connect.NewServer()
	registryv1connect.RegisterRegistryServiceHandler(server, issuer)
	rpc := registryv1connect.NewRegistryServiceClient(connect.NewClient(connectinprocess.New(server)))
	manager := NewWithClient(c, rpc, testNamespace, interval, testBackoff)
	manager.now = clk.Now
	return &fixture{manager: manager, client: c, issuer: issuer, clock: clk}
}

func placedApplication() *locoControllerV1.Application {
	return &locoControllerV1.Application{
		Name:      "resource-a",
		Namespace: testNamespace,
		Spec:      locoControllerV1.ApplicationSpec{Type: "SERVICE", WorkspaceID: testWorkspace},
	}
}

func (f *fixture) secret(t *testing.T, workspaceID string) (*corev1.Secret, error) {
	t.Helper()
	secret := &corev1.Secret{}
	name := locoControllerV1.RegistryPullSecretName(workspaceID)
	key := client.ObjectKey{Namespace: testNamespace, Name: name}
	err := f.client.Get(context.Background(), key, secret)
	return secret, err
}

func decodeAuth(t *testing.T, secret *corev1.Secret) dockerAuth {
	t.Helper()
	var config dockerConfig
	if err := json.Unmarshal(secret.Data[corev1.DockerConfigJsonKey], &config); err != nil {
		t.Fatalf("decode %s: %v", corev1.DockerConfigJsonKey, err)
	}
	if len(config.Auths) != 1 {
		t.Fatalf("auths = %v, want only %s", config.Auths, testHost)
	}
	auth, ok := config.Auths[testHost]
	if !ok {
		t.Fatalf("auths = %v, want an entry for %s", config.Auths, testHost)
	}
	return auth
}

func TestEnsureWritesAWellFormedPullSecretOnce(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, testInterval)

	if err := f.manager.Ensure(ctx, testWorkspace); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if err := f.manager.Ensure(ctx, testWorkspace); err != nil {
		t.Fatalf("second ensure: %v", err)
	}
	if calls := f.issuer.callCount(); calls != 1 {
		t.Fatalf("IssuePullCredential calls = %d, want 1", calls)
	}
	secret, err := f.secret(t, testWorkspace)
	if err != nil {
		t.Fatalf("get pull Secret: %v", err)
	}
	if secret.Type != corev1.SecretTypeDockerConfigJson {
		t.Fatalf("type = %s, want %s", secret.Type, corev1.SecretTypeDockerConfigJson)
	}
	if secret.Labels[locoControllerV1.LabelRegistryPull] != testWorkspace {
		t.Fatalf("labels = %v, want %s=%s", secret.Labels, locoControllerV1.LabelRegistryPull, testWorkspace)
	}
	wantExpiry := f.clock.Now().Add(testLifetime).Format(time.RFC3339Nano)
	if secret.Annotations[AnnotationExpiresAt] != wantExpiry {
		t.Fatalf("expires-at annotation = %q, want %q", secret.Annotations[AnnotationExpiresAt], wantExpiry)
	}
	auth := decodeAuth(t, secret)
	if auth.Username != testPullUser || auth.Password != testFirstPassword {
		t.Fatalf("credential = %q / %q, want %s / secret-1", auth.Username, auth.Password, testPullUser)
	}
	decoded, err := base64.StdEncoding.DecodeString(auth.Auth)
	if err != nil || string(decoded) != testPullUser+":secret-1" {
		t.Fatalf("auth = %q (%v), want base64 of %s:secret-1", auth.Auth, err, testPullUser)
	}
}

func TestEnsureFailsWhenTheControlPlaneRefuses(t *testing.T) {
	f := newFixture(t, testInterval)
	f.issuer.failures = 1
	f.issuer.code = connect.CodeFailedPrecondition

	err := f.manager.Ensure(context.Background(), testWorkspace)
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("ensure error = %v, want FailedPrecondition", err)
	}
	if _, getErr := f.secret(t, testWorkspace); !apierrors.IsNotFound(getErr) {
		t.Fatalf("pull Secret after a refused issue: err = %v, want not found", getErr)
	}
}

func TestCheckRenewsPastHalfTheLifetime(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, testInterval, placedApplication())
	if err := f.manager.Ensure(ctx, testWorkspace); err != nil {
		t.Fatalf("ensure: %v", err)
	}

	f.clock.advance(testLifetime/2 - time.Minute)
	if _, err := f.manager.check(ctx); err != nil {
		t.Fatalf("check before half the lifetime: %v", err)
	}
	if calls := f.issuer.callCount(); calls != 1 {
		t.Fatalf("IssuePullCredential calls before half the lifetime = %d, want 1", calls)
	}

	f.clock.advance(2 * time.Minute)
	if _, err := f.manager.check(ctx); err != nil {
		t.Fatalf("check past half the lifetime: %v", err)
	}
	if calls := f.issuer.callCount(); calls != 2 {
		t.Fatalf("IssuePullCredential calls past half the lifetime = %d, want 2", calls)
	}
	secret, err := f.secret(t, testWorkspace)
	if err != nil {
		t.Fatalf("get pull Secret: %v", err)
	}
	if auth := decodeAuth(t, secret); auth.Password != "secret-2" {
		t.Fatalf("password after renewal = %q, want secret-2", auth.Password)
	}
	wantIssued := f.clock.Now().Format(time.RFC3339Nano)
	if secret.Annotations[AnnotationIssuedAt] != wantIssued {
		t.Fatalf("issued-at = %q, want %q", secret.Annotations[AnnotationIssuedAt], wantIssued)
	}
}

func TestCheckDeletesSecretsOfWorkspacesWithNoApplication(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, testInterval, placedApplication())
	if err := f.manager.Ensure(ctx, testWorkspace); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if err := f.manager.Ensure(ctx, testOtherSpace); err != nil {
		t.Fatalf("ensure other workspace: %v", err)
	}

	if _, err := f.manager.check(ctx); err != nil {
		t.Fatalf("check within the grace period: %v", err)
	}
	if _, err := f.secret(t, testOtherSpace); err != nil {
		t.Fatalf("a Secret issued within one check interval was deleted: %v", err)
	}

	f.clock.advance(testInterval)
	if _, err := f.manager.check(ctx); err != nil {
		t.Fatalf("check: %v", err)
	}
	if _, err := f.secret(t, testOtherSpace); !apierrors.IsNotFound(err) {
		t.Fatalf("Secret of a workspace with no Application: err = %v, want not found", err)
	}
	if _, err := f.secret(t, testWorkspace); err != nil {
		t.Fatalf("Secret of a placed workspace: %v", err)
	}
}

func TestCheckWritesAMissingSecretForAPlacedWorkspace(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, testInterval, placedApplication())

	if _, err := f.manager.check(ctx); err != nil {
		t.Fatalf("check: %v", err)
	}
	secret, err := f.secret(t, testWorkspace)
	if err != nil {
		t.Fatalf("pull Secret of a workspace with an Application: %v", err)
	}
	if auth := decodeAuth(t, secret); auth.Password != testFirstPassword {
		t.Fatalf("password = %q, want secret-1", auth.Password)
	}
	if _, err := f.manager.check(ctx); err != nil {
		t.Fatalf("second check: %v", err)
	}
	if calls := f.issuer.callCount(); calls != 1 {
		t.Fatalf("IssuePullCredential calls = %d, want 1", calls)
	}
}

func runManager(t *testing.T, f *fixture) <-chan struct{} {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		f.manager.Run(ctx)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	return done
}

func TestRunBacksOffAndRetriesAfterAnError(t *testing.T) {
	stale := &corev1.Secret{
		Name:      locoControllerV1.RegistryPullSecretName(testWorkspace),
		Namespace: testNamespace,
		Labels:    map[string]string{locoControllerV1.LabelRegistryPull: testWorkspace},
	}
	f := newFixture(t, testWait, stale, placedApplication())
	f.issuer.failures = 2
	f.issuer.code = connect.CodeUnavailable

	runManager(t, f)
	deadline := time.Now().Add(testWait)
	for {
		secret, err := f.secret(t, testWorkspace)
		if err == nil && secret.Type == corev1.SecretTypeDockerConfigJson {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the Secret was not renewed after the outage: calls = %d", f.issuer.callCount())
		}
		time.Sleep(testPoll)
	}
	if calls := f.issuer.callCount(); calls != 3 {
		t.Fatalf("IssuePullCredential calls = %d, want two failures and one success", calls)
	}
}

func TestRunKeepsCheckingWhileTheControlPlaneIsInStaticMode(t *testing.T) {
	f := newFixture(t, time.Hour, placedApplication())
	f.issuer.failures = 3
	f.issuer.code = connect.CodeFailedPrecondition

	done := runManager(t, f)
	deadline := time.Now().Add(testWait)
	for {
		if _, err := f.secret(t, testWorkspace); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no pull Secret after FailedPrecondition cleared: calls = %d", f.issuer.callCount())
		}
		time.Sleep(testPoll)
	}
	select {
	case <-done:
		t.Fatal("Run returned after FailedPrecondition")
	default:
	}
	if calls := f.issuer.callCount(); calls != f.issuer.failures+1 {
		t.Fatalf("IssuePullCredential calls = %d, want %d", calls, f.issuer.failures+1)
	}
}

func TestRunRenewsALifetimeShorterThanTwoIntervals(t *testing.T) {
	f := newFixture(t, time.Hour, placedApplication())
	f.manager.now = time.Now
	f.issuer.now = time.Now
	f.issuer.lifetime = testShortLifetime

	runManager(t, f)
	deadline := time.Now().Add(testWait)
	for f.issuer.callCount() < testRenewals {
		if time.Now().After(deadline) {
			t.Fatalf("IssuePullCredential calls = %d within %s, want %d", f.issuer.callCount(), testWait, testRenewals)
		}
		time.Sleep(testPoll)
	}
}

func TestEnsureWaitsForACheckThatDeletesTheSecret(t *testing.T) {
	ctx := context.Background()
	listing := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	funcs := interceptor.Funcs{
		List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
			if _, ok := list.(*locoControllerV1.ApplicationList); ok {
				once.Do(func() {
					close(listing)
					<-release
				})
			}
			return c.List(ctx, list, opts...)
		},
	}
	f := newInterceptedFixture(t, testInterval, funcs)
	if err := f.manager.Ensure(ctx, testWorkspace); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	f.clock.advance(testInterval)

	checked := make(chan error, 1)
	go func() {
		_, err := f.manager.check(ctx)
		checked <- err
	}()
	<-listing
	ensured := make(chan error, 1)
	go func() {
		ensured <- f.manager.Ensure(ctx, testWorkspace)
	}()
	select {
	case err := <-ensured:
		t.Fatalf("Ensure returned (%v) while a check was running", err)
	case <-time.After(testSettle):
	}
	close(release)
	if err := <-checked; err != nil {
		t.Fatalf("check: %v", err)
	}
	if err := <-ensured; err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if _, err := f.secret(t, testWorkspace); err != nil {
		t.Fatalf("pull Secret after Ensure raced a check: %v", err)
	}
}

func TestARefusedWorkspaceDoesNotFailTheCheck(t *testing.T) {
	ctx := context.Background()
	other := placedApplication()
	other.Name = "resource-b"
	other.Spec.WorkspaceID = testOtherSpace
	deleting := placedApplication()
	deleting.Name = "resource-c"
	deleting.Spec.WorkspaceID = "01a66666-6666-7666-8666-666666666666"
	deleting.Finalizers = []string{"loco.io/test"}
	f := newFixture(t, testInterval, placedApplication(), other, deleting)
	if err := f.client.Delete(ctx, deleting); err != nil {
		t.Fatalf("mark Application for deletion: %v", err)
	}
	f.issuer.refused = map[string]bool{testOtherSpace: true}

	if _, err := f.manager.check(ctx); err != nil {
		t.Fatalf("check with one refused workspace: %v", err)
	}
	if _, err := f.secret(t, testWorkspace); err != nil {
		t.Fatalf("pull Secret of an accepted workspace: %v", err)
	}
	if _, err := f.secret(t, testOtherSpace); !apierrors.IsNotFound(err) {
		t.Fatalf("pull Secret of a refused workspace: err = %v, want not found", err)
	}
	if _, err := f.secret(t, deleting.Spec.WorkspaceID); !apierrors.IsNotFound(err) {
		t.Fatalf("pull Secret of a workspace whose Application is being deleted: err = %v, want not found", err)
	}
}

func TestEnsureOverwritesASecretWithoutThePullLabel(t *testing.T) {
	ctx := context.Background()
	foreign := &corev1.Secret{
		Name:      locoControllerV1.RegistryPullSecretName(testWorkspace),
		Namespace: testNamespace,
		Data:      map[string][]byte{"other": []byte("value")},
	}
	f := newFixture(t, testInterval, foreign)

	if err := f.manager.Ensure(ctx, testWorkspace); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	secret, err := f.secret(t, testWorkspace)
	if err != nil {
		t.Fatalf("get pull Secret: %v", err)
	}
	if secret.Labels[locoControllerV1.LabelRegistryPull] != testWorkspace {
		t.Fatalf("labels = %v, want %s=%s", secret.Labels, locoControllerV1.LabelRegistryPull, testWorkspace)
	}
	if auth := decodeAuth(t, secret); auth.Password != testFirstPassword {
		t.Fatalf("password = %q, want secret-1", auth.Password)
	}
	if _, ok := secret.Data["other"]; ok {
		t.Fatalf("data = %v, want only %s", secret.Data, corev1.DockerConfigJsonKey)
	}
}
