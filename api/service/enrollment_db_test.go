package service

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
	"github.com/google/uuid"

	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	"github.com/team-loco/loco/gen/go/loco/agent/v1/agentv1connect"
	clusterv1 "github.com/team-loco/loco/gen/go/loco/cluster/v1"
	environmentv1 "github.com/team-loco/loco/gen/go/loco/environment/v1"
	"github.com/team-loco/loco/internal/clusterassertion"
)

const concurrentEnrollments = 8

type enrollmentFixture struct {
	*deployFixture
	admin *ClusterAdminServer
	agent agentv1connect.AgentServiceClient
}

func newEnrollmentFixture(t *testing.T) *enrollmentFixture {
	t.Helper()
	f := newDeployFixture(t)
	return &enrollmentFixture{
		deployFixture: f,
		admin:         NewClusterAdminServer(f.pool, f.queries, testAgentConfig().JoinTokenTTL),
		agent:         startSyncServer(t, f),
	}
}

func newAgentKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func publicKeyBytes(t *testing.T, key ed25519.PrivateKey) []byte {
	t.Helper()
	public, ok := key.Public().(ed25519.PublicKey)
	if !ok {
		t.Fatal("ed25519 private key returned a non-ed25519 public key")
	}
	return public
}

func (e *enrollmentFixture) createCluster(t *testing.T) (uuid.UUID, string) {
	t.Helper()
	resp, err := e.admin.CreateCluster(systemAdminContext(t), &clusterv1.CreateClusterRequest{
		Name: "enrolled", Region: testRegion, Tier: environmentv1.EnvironmentType_ENVIRONMENT_TYPE_PRODUCTION,
	})
	if err != nil {
		t.Fatal(err)
	}
	return uuid.MustParse(resp.GetClusterId()), resp.GetJoinToken().GetToken()
}

func (e *enrollmentFixture) issueToken(t *testing.T, clusterID uuid.UUID) string {
	t.Helper()
	resp, err := e.admin.IssueJoinToken(
		systemAdminContext(t),
		&clusterv1.IssueJoinTokenRequest{ClusterId: clusterID.String()},
	)
	if err != nil {
		t.Fatal(err)
	}
	return resp.GetJoinToken().GetToken()
}

func (e *enrollmentFixture) enroll(
	t *testing.T,
	token string,
	clusterKey uuid.UUID,
	key ed25519.PrivateKey,
) (*agentv1.RegisterResponse, error) {
	t.Helper()
	return e.agent.Register(t.Context(), &agentv1.RegisterRequest{
		ClusterKey: clusterKey.String(),
		Enrollment: &agentv1.Enrollment{JoinToken: token, PublicKey: publicKeyBytes(t, key)},
	})
}

func signAssertion(t *testing.T, key ed25519.PrivateKey, clusterID uuid.UUID) string {
	t.Helper()
	token, err := clusterassertion.Sign(key, clusterID.String(), time.Now(), testAssertionLifetime)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func registerWithAssertion(
	ctx context.Context,
	client agentv1connect.AgentServiceClient,
	assertion string,
	clusterKey uuid.UUID,
) (*agentv1.RegisterResponse, error) {
	ctx, info := connect.NewClientContext(ctx)
	info.RequestHeader().Set("Authorization", "Bearer "+assertion)
	return client.Register(ctx, &agentv1.RegisterRequest{ClusterKey: clusterKey.String()})
}

func (e *enrollmentFixture) register(
	t *testing.T,
	clusterID uuid.UUID,
	clusterKey uuid.UUID,
	key ed25519.PrivateKey,
) error {
	t.Helper()
	assertion := signAssertion(t, key, clusterID)
	_, err := registerWithAssertion(t.Context(), e.agent, assertion, clusterKey)
	return err
}

func (e *enrollmentFixture) fingerprint(t *testing.T, clusterID uuid.UUID) string {
	t.Helper()
	listed, err := e.admin.ListClusters(systemAdminContext(t), &clusterv1.ListClustersRequest{})
	if err != nil {
		t.Fatal(err)
	}
	for _, cluster := range listed.GetClusters() {
		if cluster.GetId() == clusterID.String() {
			return cluster.GetAgentKeyFingerprint()
		}
	}
	t.Fatalf("cluster %s not listed", clusterID)
	return ""
}

func keyFingerprint(t *testing.T, key ed25519.PrivateKey) string {
	t.Helper()
	sum := sha256.Sum256(publicKeyBytes(t, key))
	return hex.EncodeToString(sum[:])
}

func syncError(
	ctx context.Context,
	t *testing.T,
	client agentv1connect.AgentServiceClient,
	assertion string,
) error {
	t.Helper()
	ctx, info := connect.NewClientContext(ctx)
	info.RequestHeader().Set("Authorization", "Bearer "+assertion)
	stream, err := client.Sync(ctx)
	if err != nil {
		return err
	}
	if sendErr := stream.Send(&agentv1.SyncRequest{}); sendErr != nil {
		t.Logf("send: %v", sendErr)
	}
	_, err = stream.Receive()
	return err
}

func requireCode(t *testing.T, err error, want connect.Code, what string) {
	t.Helper()
	if connect.CodeOf(err) != want {
		t.Fatalf("%s: %v, want %v", what, err, want)
	}
}

func TestJoinTokenEnrollsExactlyOneAgentUnderConcurrency(t *testing.T) {
	e := newEnrollmentFixture(t)
	clusterID, token := e.createCluster(t)
	if fingerprint := e.fingerprint(t, clusterID); fingerprint != "" {
		t.Fatalf("pending cluster has key fingerprint %q", fingerprint)
	}
	clusterKey := uuid.New()
	keys := make([]ed25519.PrivateKey, concurrentEnrollments)
	errs := make([]error, concurrentEnrollments)
	var wg sync.WaitGroup
	for i := range keys {
		keys[i] = newAgentKey(t)
		wg.Go(func() {
			resp, err := e.enroll(t, token, clusterKey, keys[i])
			if err == nil && resp.GetClusterId() != clusterID.String() {
				t.Errorf("enrolled into %s, want %s", resp.GetClusterId(), clusterID)
			}
			errs[i] = err
		})
	}
	wg.Wait()
	var winner ed25519.PrivateKey
	var losers []ed25519.PrivateKey
	for i, err := range errs {
		if err == nil {
			if winner != nil {
				t.Fatal("a join token enrolled two agents")
			}
			winner = keys[i]
			continue
		}
		requireCode(t, err, connect.CodeUnauthenticated, "losing enrollment")
		losers = append(losers, keys[i])
	}
	if winner == nil {
		t.Fatal("no enrollment succeeded")
	}
	if got := e.fingerprint(t, clusterID); got != keyFingerprint(t, winner) {
		t.Fatalf("fingerprint %q, want the winning key's", got)
	}
	if _, err := e.enroll(t, token, clusterKey, winner); err != nil {
		t.Fatalf("retry by the enrolled key: %v", err)
	}
	_, err := e.enroll(t, token, clusterKey, newAgentKey(t))
	requireCode(t, err, connect.CodeUnauthenticated, "second enrollment with a used token")
	if err := e.register(t, clusterID, clusterKey, winner); err != nil {
		t.Fatalf("enrolled agent restart: %v", err)
	}
	requireCode(t, e.register(t, clusterID, clusterKey, losers[0]), connect.CodeUnauthenticated, "unbound key")
}

func TestEnrollmentRejectsExpiredUnknownAndForeignTokens(t *testing.T) {
	e := newEnrollmentFixture(t)
	ctx := t.Context()
	clusterID, token := e.createCluster(t)
	if _, err := e.pool.Exec(
		ctx,
		"UPDATE clusters SET join_token_expires_at = NOW() WHERE id = $1",
		clusterID,
	); err != nil {
		t.Fatal(err)
	}
	clusterKey := uuid.New()
	key := newAgentKey(t)
	_, err := e.enroll(t, token, clusterKey, key)
	requireCode(t, err, connect.CodeUnauthenticated, "expired token")
	_, err = e.enroll(t, joinTokenPrefix+"unknown", clusterKey, key)
	requireCode(t, err, connect.CodeUnauthenticated, "unknown token")

	token = e.issueToken(t, clusterID)
	if _, enrollErr := e.enroll(t, token, clusterKey, key); enrollErr != nil {
		t.Fatal(enrollErr)
	}
	rotation := e.issueToken(t, clusterID)
	_, err = e.enroll(t, rotation, uuid.New(), newAgentKey(t))
	requireCode(t, err, connect.CodeUnauthenticated, "token used from another Kubernetes cluster")

	otherID, otherToken := e.createCluster(t)
	_, err = e.enroll(t, otherToken, clusterKey, newAgentKey(t))
	requireCode(t, err, connect.CodeFailedPrecondition, "Kubernetes cluster enrolled twice")

	requireCode(t, e.register(t, clusterID, uuid.New(), key), connect.CodeFailedPrecondition, "moved key")
	requireCode(t, e.register(t, otherID, clusterKey, key), connect.CodeUnauthenticated, "pending cluster")
	requireCode(
		t,
		e.register(t, e.otherCluster, e.otherCluster, key),
		connect.CodeUnauthenticated,
		"other cluster's key",
	)
}

func TestReEnrollmentRotatesTheKeyAndClosesTheStream(t *testing.T) {
	e := newEnrollmentFixture(t)
	clusterID, token := e.createCluster(t)
	clusterKey := uuid.New()
	original := newAgentKey(t)
	if _, err := e.enroll(t, token, clusterKey, original); err != nil {
		t.Fatal(err)
	}
	stream := openSyncWithAssertion(t.Context(), t, e.agent, signAssertion(t, original, clusterID))
	select {
	case result := <-stream:
		t.Fatalf("unexpected stream result: %+v", result)
	case <-time.After(100 * time.Millisecond):
	}
	replacement := newAgentKey(t)
	if _, err := e.enroll(t, e.issueToken(t, clusterID), clusterKey, replacement); err != nil {
		t.Fatal(err)
	}
	requireCode(t, nextResult(t, stream).err, connect.CodeAborted, "stream of the rotated key")
	requireCode(t, e.register(t, clusterID, clusterKey, original), connect.CodeUnauthenticated, "rotated key")
	if err := e.register(t, clusterID, clusterKey, replacement); err != nil {
		t.Fatal(err)
	}
	if got := e.fingerprint(t, clusterID); got != keyFingerprint(t, replacement) {
		t.Fatalf("fingerprint %q, want the replacement key's", got)
	}
}

func TestRemovedClusterIsRevoked(t *testing.T) {
	e := newEnrollmentFixture(t)
	ctx := t.Context()
	stream := openSync(ctx, t, e.agent, e.clusterID)
	select {
	case result := <-stream:
		t.Fatalf("unexpected stream result: %+v", result)
	case <-time.After(100 * time.Millisecond):
	}
	unused := e.issueToken(t, e.clusterID)
	for _, operation := range []string{ClusterPause, ClusterRemove} {
		if _, err := ManageCluster(ctx, e.pool, operation, e.clusterID, false); err != nil {
			t.Fatal(err)
		}
	}
	requireCode(t, nextResult(t, stream).err, connect.CodePermissionDenied, "stream of a removed cluster")
	_, err := registerWithAssertion(ctx, e.agent, testAssertion(t, e.clusterID), e.clusterID)
	requireCode(t, err, connect.CodeUnauthenticated, "registration of a removed cluster")
	syncErr := syncError(ctx, t, e.agent, testAssertion(t, e.clusterID))
	requireCode(t, syncErr, connect.CodeUnauthenticated, "sync of a removed cluster")
	_, err = e.enroll(t, unused, e.clusterID, newAgentKey(t))
	requireCode(t, err, connect.CodeUnauthenticated, "enrollment into a removed cluster")
	_, err = e.admin.IssueJoinToken(
		systemAdminContext(t),
		&clusterv1.IssueJoinTokenRequest{ClusterId: e.clusterID.String()},
	)
	requireCode(t, err, connect.CodeFailedPrecondition, "join token for a removed cluster")
	if _, restoreErr := ManageCluster(ctx, e.pool, ClusterRestore, e.clusterID, false); restoreErr != nil {
		t.Fatal(restoreErr)
	}
	_, err = e.enroll(t, unused, e.clusterID, newAgentKey(t))
	requireCode(t, err, connect.CodeUnauthenticated, "join token issued before removal, after restore")
	if _, err := registerWithAssertion(ctx, e.agent, testAssertion(t, e.clusterID), e.clusterID); err != nil {
		t.Fatalf("restored cluster keeps its agent key: %v", err)
	}
}

func TestAssertionChecks(t *testing.T) {
	e := newEnrollmentFixture(t)
	ctx := t.Context()
	replica := startSyncServer(t, e.deployFixture)
	key := testClusterKey(e.clusterID)
	now := time.Now()
	sign := func(claims jwt.Claims) string {
		t.Helper()
		signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.EdDSA, Key: key}, nil)
		if err != nil {
			t.Fatal(err)
		}
		token, err := jwt.Signed(signer).Claims(claims).Serialize()
		if err != nil {
			t.Fatal(err)
		}
		return token
	}
	claims := func(audience string, issuedAt time.Time, lifetime time.Duration) jwt.Claims {
		return jwt.Claims{
			Issuer:   e.clusterID.String(),
			Subject:  e.clusterID.String(),
			Audience: jwt.Audience{audience},
			IssuedAt: jwt.NewNumericDate(issuedAt),
			Expiry:   jwt.NewNumericDate(issuedAt.Add(lifetime)),
			ID:       rand.Text(),
		}
	}
	maxLifetime := testAgentConfig().AssertionMaxLifetime
	for name, assertion := range map[string]string{
		"wrong audience": sign(claims("other", now, testAssertionLifetime)),
		"expired":        sign(claims(clusterassertion.Audience, now.Add(-time.Hour), testAssertionLifetime)),
		"too long-lived": sign(claims(clusterassertion.Audience, now, 2*maxLifetime)),
		"wrong key":      signAssertion(t, testClusterKey(e.otherCluster), e.clusterID),
		"not a JWT":      "not-a-jwt",
	} {
		_, err := registerWithAssertion(ctx, e.agent, assertion, e.clusterID)
		requireCode(t, err, connect.CodeUnauthenticated, name)
	}
	assertion := testAssertion(t, e.clusterID)
	if _, err := registerWithAssertion(ctx, e.agent, assertion, e.clusterID); err != nil {
		t.Fatal(err)
	}
	_, err := registerWithAssertion(ctx, e.agent, assertion, e.clusterID)
	requireCode(t, err, connect.CodeUnauthenticated, "replay on the same replica")
	_, err = registerWithAssertion(ctx, replica, assertion, e.clusterID)
	requireCode(t, err, connect.CodeUnauthenticated, "replay on another replica")
	requireCode(t, syncError(ctx, t, replica, assertion), connect.CodeUnauthenticated, "replayed sync")
}
