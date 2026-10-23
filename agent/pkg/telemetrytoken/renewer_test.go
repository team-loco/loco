package telemetrytoken

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectinprocess"
	"google.golang.org/protobuf/types/known/timestamppb"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes/fake"

	ingestv1 "github.com/team-loco/loco/gen/go/loco/ingest/v1"
	"github.com/team-loco/loco/gen/go/loco/ingest/v1/ingestv1connect"
)

const (
	testNamespace     = "observability"
	testSecretName    = "otlp-exporter"
	testSecretKey     = "token"
	testOtherKey      = "other"
	testOtherValue    = "kept"
	testRetryDelay    = 10 * time.Millisecond
	testShortLifetime = 200 * time.Millisecond
	testLongLifetime  = time.Hour
	testWait          = 5 * time.Second
	testPoll          = 5 * time.Millisecond
	testOutage        = "control plane unavailable"
)

type step struct {
	lifetime time.Duration
	fail     bool
}

type issuance struct {
	token     string
	at        time.Time
	expiresAt time.Time
}

type scriptedIssuer struct {
	ingestv1connect.UnimplementedIngestServiceHandler
	mu     sync.Mutex
	script []step
	calls  int
	issued []issuance
}

func (s *scriptedIssuer) IssueToken(
	context.Context,
	*ingestv1.IssueTokenRequest,
) (*ingestv1.IssueTokenResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := step{lifetime: testLongLifetime}
	if s.calls < len(s.script) {
		next = s.script[s.calls]
	}
	s.calls++
	if next.fail {
		return nil, connect.NewError(connect.CodeUnavailable, testOutage)
	}
	now := time.Now()
	token := "token-" + strconv.Itoa(s.calls)
	expiresAt := now.Add(next.lifetime)
	s.issued = append(s.issued, issuance{token: token, at: now, expiresAt: expiresAt})
	return &ingestv1.IssueTokenResponse{Token: token, ExpiresAt: timestamppb.New(expiresAt)}, nil
}

func (s *scriptedIssuer) snapshot() []issuance {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]issuance(nil), s.issued...)
}

func testBackoff() wait.Backoff {
	return wait.Backoff{Duration: testRetryDelay, Factor: 1, Steps: 1 << 30, Cap: testRetryDelay}
}

func existingSecret() *corev1.Secret {
	return &corev1.Secret{
		Name:      testSecretName,
		Namespace: testNamespace,
		Data:      map[string][]byte{testOtherKey: []byte(testOtherValue)},
	}
}

func startRenewer(t *testing.T, issuer *scriptedIssuer, kube *fake.Clientset) {
	t.Helper()
	server := connect.NewServer()
	ingestv1connect.RegisterIngestServiceHandler(server, issuer)
	client := ingestv1connect.NewIngestServiceClient(connect.NewClient(connectinprocess.New(server)))
	secret := Secret{Namespace: testNamespace, Name: testSecretName, Key: testSecretKey}
	renewer := NewRenewer(client, kube, secret, testBackoff)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		renewer.Run(ctx)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
}

func storedToken(t *testing.T, kube *fake.Clientset) string {
	t.Helper()
	secret, err := kube.CoreV1().Secrets(testNamespace).Get(t.Context(), testSecretName, metav1.GetOptions{})
	if err != nil {
		return ""
	}
	if string(secret.Data[testOtherKey]) != testOtherValue {
		t.Fatalf("renewal dropped key %s from the Secret", testOtherKey)
	}
	return string(secret.Data[testSecretKey])
}

func waitForIssuances(t *testing.T, issuer *scriptedIssuer, count int) []issuance {
	t.Helper()
	deadline := time.Now().Add(testWait)
	for time.Now().Before(deadline) {
		if issued := issuer.snapshot(); len(issued) >= count {
			return issued
		}
		time.Sleep(testPoll)
	}
	t.Fatalf("timed out waiting for %d issued tokens, got %d", count, len(issuer.snapshot()))
	return nil
}

func waitForStoredToken(t *testing.T, kube *fake.Clientset, token string) {
	t.Helper()
	deadline := time.Now().Add(testWait)
	for time.Now().Before(deadline) {
		if storedToken(t, kube) == token {
			return
		}
		time.Sleep(testPoll)
	}
	t.Fatalf("Secret holds %q, want %q", storedToken(t, kube), token)
}

func TestRenewalDelayIsHalfTheRemainingLifetime(t *testing.T) {
	now := time.Now()
	if got := RenewalDelay(now, now.Add(time.Hour)); got != 30*time.Minute {
		t.Fatalf("RenewalDelay = %s, want 30m", got)
	}
}

func TestRenewerWritesEachTokenBeforeThePreviousExpires(t *testing.T) {
	issuer := &scriptedIssuer{script: []step{{lifetime: testShortLifetime}, {lifetime: testShortLifetime}}}
	kube := fake.NewClientset(existingSecret())
	startRenewer(t, issuer, kube)

	issued := waitForIssuances(t, issuer, 3)
	for i := 1; i < len(issued); i++ {
		if !issued[i].at.Before(issued[i-1].expiresAt) {
			t.Fatalf("token %d issued at %s, after token %d expired at %s",
				i, issued[i].at, i-1, issued[i-1].expiresAt)
		}
	}
	waitForStoredToken(t, kube, issued[2].token)
}

func TestRenewerRetriesAFailedRenewal(t *testing.T) {
	issuer := &scriptedIssuer{script: []step{{fail: true}, {fail: true}, {fail: true}}}
	kube := fake.NewClientset(existingSecret())
	startRenewer(t, issuer, kube)

	issued := waitForIssuances(t, issuer, 1)
	waitForStoredToken(t, kube, issued[0].token)
}

func TestRenewerRecoversAfterTheTokenExpired(t *testing.T) {
	outage := make([]step, 0, int(testShortLifetime/testRetryDelay)*2+1)
	outage = append(outage, step{lifetime: testShortLifetime})
	for range cap(outage) - 1 {
		outage = append(outage, step{fail: true})
	}
	issuer := &scriptedIssuer{script: outage}
	kube := fake.NewClientset(existingSecret())
	startRenewer(t, issuer, kube)

	issued := waitForIssuances(t, issuer, 2)
	if !issued[1].at.After(issued[0].expiresAt) {
		t.Fatalf("recovery token issued at %s, before the first expired at %s", issued[1].at, issued[0].expiresAt)
	}
	waitForStoredToken(t, kube, issued[1].token)
}

func TestRenewerRetriesUntilTheSecretExists(t *testing.T) {
	issuer := &scriptedIssuer{}
	kube := fake.NewClientset()
	startRenewer(t, issuer, kube)

	waitForIssuances(t, issuer, 2)
	secrets := kube.CoreV1().Secrets(testNamespace)
	if _, err := secrets.Create(t.Context(), existingSecret(), metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(testWait)
	for storedToken(t, kube) == "" && time.Now().Before(deadline) {
		time.Sleep(testPoll)
	}
	if storedToken(t, kube) == "" {
		t.Fatal("renewer never wrote the token once the Secret existed")
	}
}
