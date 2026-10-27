package service

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectinprocess"
	"github.com/go-jose/go-jose/v4"
	"github.com/google/uuid"

	genDb "github.com/team-loco/loco/api/gen/db"
	ingestv1 "github.com/team-loco/loco/gen/go/loco/ingest/v1"
	"github.com/team-loco/loco/gen/go/loco/ingest/v1/ingestv1connect"
	"github.com/team-loco/loco/internal/ingesttoken"
	"github.com/team-loco/loco/internal/ingesttoken/ingesttest"
)

const (
	testIngestTokenLifetime = 10 * time.Minute
	testCurrentIngestKey    = "v2"
	testPreviousIngestKey   = "v1"
)

var errTransitDown = errors.New("transit unavailable")

type testIngestSigner struct {
	signers []ingesttest.Signer
	down    bool
}

func (s *testIngestSigner) KeyID(ctx context.Context) (string, error) {
	if s.down {
		return "", errTransitDown
	}
	return s.signers[0].KeyID(ctx)
}

func (s *testIngestSigner) Sign(ctx context.Context, keyID string, input []byte) ([]byte, error) {
	return s.signers[0].Sign(ctx, keyID, input)
}

func (s *testIngestSigner) PublicKeys(context.Context) (map[string]ed25519.PublicKey, error) {
	if s.down {
		return nil, errTransitDown
	}
	keys := map[string]ed25519.PublicKey{}
	for _, key := range ingesttest.KeySet(s.signers).Keys {
		public, ok := key.Key.(ed25519.PublicKey)
		if !ok {
			return nil, errTransitDown
		}
		keys[key.KeyID] = public
	}
	return keys, nil
}

func newIngestKey(t *testing.T, id string) ingesttest.Signer {
	t.Helper()
	signer, err := ingesttest.NewSigner(id)
	if err != nil {
		t.Fatal(err)
	}
	return signer
}

func newIngestServer(f *deployFixture, signer *testIngestSigner) *IngestServer {
	clusters := NewClusterAuthenticator(f.queries, testAgentConfig().AssertionMaxLifetime)
	return NewIngestServer(f.queries, clusters, signer, testIngestTokenLifetime)
}

func signingWith(keys ...ingesttest.Signer) *testIngestSigner {
	return &testIngestSigner{signers: keys}
}

func ingestClient(server *IngestServer) ingestv1connect.IngestServiceClient {
	rpcServer := connect.NewServer()
	ingestv1connect.RegisterIngestServiceHandler(rpcServer, server)
	transport := connectinprocess.New(rpcServer)
	return ingestv1connect.NewIngestServiceClient(connect.NewClient(transport))
}

func issueIngestToken(
	t *testing.T,
	client ingestv1connect.IngestServiceClient,
	assertion string,
) (*ingestv1.IssueTokenResponse, error) {
	t.Helper()
	ctx, info := connect.NewClientContext(t.Context())
	info.RequestHeader().Set("Authorization", "Bearer "+assertion)
	return client.IssueToken(ctx, &ingestv1.IssueTokenRequest{})
}

func placementWindows(
	t *testing.T,
	client ingestv1connect.IngestServiceClient,
	token string,
) ([]*ingestv1.PlacementWindow, error) {
	t.Helper()
	ctx, info := connect.NewClientContext(t.Context())
	info.RequestHeader().Set("Authorization", "Bearer "+token)
	resp, err := client.ListPlacementWindows(ctx, &ingestv1.ListPlacementWindowsRequest{})
	return resp.GetWindows(), err
}

func signedIngestToken(t *testing.T, key ingesttest.Signer, clusterID uuid.UUID, issuedAt time.Time) string {
	t.Helper()
	token, _, err := ingesttoken.Sign(t.Context(), key, clusterID.String(), issuedAt, testIngestTokenLifetime)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func TestIngestTokenExchange(t *testing.T) {
	f := newDeployFixture(t)
	key := newIngestKey(t, testCurrentIngestKey)
	client := ingestClient(newIngestServer(f, signingWith(key)))

	assertion := testAssertion(t, f.clusterID)
	before := time.Now()
	resp, err := issueIngestToken(t, client, assertion)
	if err != nil {
		t.Fatal(err)
	}
	keySet := ingesttest.KeySet([]ingesttest.Signer{key})
	claims, err := ingesttoken.Verify(resp.GetToken(), keySet, time.Now())
	if err != nil {
		t.Fatalf("issued token does not verify: %v", err)
	}
	if claims.ClusterID != f.clusterID.String() {
		t.Fatalf("sub = %s, want %s", claims.ClusterID, f.clusterID)
	}
	lifetime := resp.GetExpiresAt().AsTime().Sub(before)
	if lifetime < testIngestTokenLifetime-time.Second || lifetime > testIngestTokenLifetime+time.Second {
		t.Fatalf("token lifetime = %s, want %s", lifetime, testIngestTokenLifetime)
	}

	_, err = issueIngestToken(t, client, assertion)
	requireCode(t, err, connect.CodeUnauthenticated, "replayed assertion")
	_, err = issueIngestToken(t, client, signAssertion(t, testClusterKey(f.otherCluster), f.clusterID))
	requireCode(t, err, connect.CodeUnauthenticated, "assertion signed by another cluster's key")

	for _, operation := range []string{ClusterPause, ClusterRemove} {
		if _, opErr := ManageCluster(t.Context(), f.pool, operation, f.clusterID, false); opErr != nil {
			t.Fatal(opErr)
		}
	}
	_, err = issueIngestToken(t, client, testAssertion(t, f.clusterID))
	requireCode(t, err, connect.CodeUnauthenticated, "removed cluster")

	down := ingestClient(newIngestServer(f, &testIngestSigner{signers: []ingesttest.Signer{key}, down: true}))
	_, err = issueIngestToken(t, down, testAssertion(t, f.otherCluster))
	requireCode(t, err, connect.CodeUnavailable, "signer unavailable")
}

func TestIngestJWKSPublishesCurrentAndPreviousKeys(t *testing.T) {
	f := newDeployFixture(t)
	previous := newIngestKey(t, testPreviousIngestKey)
	current := newIngestKey(t, testCurrentIngestKey)
	beforeRotation := ingestClient(newIngestServer(f, signingWith(previous)))
	rotatedServer := newIngestServer(f, signingWith(current, previous))
	rotated := ingestClient(rotatedServer)
	retired := ingestClient(newIngestServer(f, signingWith(current)))

	oldToken, err := issueIngestToken(t, beforeRotation, testAssertion(t, f.clusterID))
	if err != nil {
		t.Fatal(err)
	}
	newToken, err := issueIngestToken(t, rotated, testAssertion(t, f.clusterID))
	if err != nil {
		t.Fatal(err)
	}
	currentSet := ingesttest.KeySet([]ingesttest.Signer{current})
	newClaims, err := ingesttoken.Verify(newToken.GetToken(), currentSet, time.Now())
	if err != nil {
		t.Fatalf("token issued after rotation is not signed by the current key: %v", err)
	}
	if newClaims.ClusterID != f.clusterID.String() {
		t.Fatalf("sub = %s", newClaims.ClusterID)
	}
	if _, rotationErr := placementWindows(t, rotated, oldToken.GetToken()); rotationErr != nil {
		t.Fatalf("token from the previous key during rotation: %v", rotationErr)
	}
	_, err = placementWindows(t, retired, oldToken.GetToken())
	requireCode(t, err, connect.CodeUnauthenticated, "token from a retired key")

	recorder := httptest.NewRecorder()
	rotatedServer.JWKS(recorder, httptest.NewRequestWithContext(t.Context(), http.MethodGet, ingesttoken.JWKSPath, nil))
	var published jose.JSONWebKeySet
	if err := json.Unmarshal(recorder.Body.Bytes(), &published); err != nil {
		t.Fatalf("decode JWKS: %v", err)
	}
	if len(published.Keys) != 2 || len(published.Key(testCurrentIngestKey)) != 1 ||
		len(published.Key(testPreviousIngestKey)) != 1 {
		t.Fatalf("JWKS = %s", recorder.Body.String())
	}
	for _, key := range published.Keys {
		if !key.IsPublic() {
			t.Fatalf("JWKS publishes private key %s", key.KeyID)
		}
	}
	if _, err := ingesttoken.Verify(oldToken.GetToken(), published, time.Now()); err != nil {
		t.Fatalf("published JWKS does not verify the previous key's token: %v", err)
	}
}

func windowSummary(windows []*ingestv1.PlacementWindow) (open, closed int) {
	for _, window := range windows {
		if window.GetRemovedAt() == nil {
			open++
			continue
		}
		closed++
	}
	return open, closed
}

func TestPlacementWindowsFollowPlacements(t *testing.T) {
	f := newDeployFixture(t)
	ctx := t.Context()
	key := newIngestKey(t, testCurrentIngestKey)
	client := ingestClient(newIngestServer(f, signingWith(key)))
	token := signedIngestToken(t, key, f.clusterID, time.Now())

	windows, err := placementWindows(t, client, token)
	if err != nil || len(windows) != 0 {
		t.Fatalf("windows before any placement = %v, %v", windows, err)
	}
	for range 2 {
		if _, deployErr := f.deploy(ctx, staticSpec); deployErr != nil {
			t.Fatal(deployErr)
		}
	}
	windows, err = placementWindows(t, client, token)
	if err != nil {
		t.Fatal(err)
	}
	if open, closed := windowSummary(windows); open != 1 || closed != 0 {
		t.Fatalf("after two deploys: %d open, %d closed windows, want one open", open, closed)
	}
	var wantWorkspace uuid.UUID
	row := f.pool.QueryRow(ctx, "SELECT workspace_id FROM resources WHERE id = $1", f.resourceID)
	if scanErr := row.Scan(&wantWorkspace); scanErr != nil {
		t.Fatal(scanErr)
	}
	if windows[0].GetWorkspaceId() != wantWorkspace.String() {
		t.Fatalf("window workspace = %s, want %s", windows[0].GetWorkspaceId(), wantWorkspace)
	}

	if removeErr := withTx(ctx, f.pool, func(qtx *genDb.Queries) error {
		return removePlacement(ctx, qtx, f.resourceID, f.clusterID)
	}); removeErr != nil {
		t.Fatal(removeErr)
	}
	placement := f.placement(t, f.clusterID)
	applied := genDb.DeleteAppliedPlacementParams{
		ID: placement.ID, ClusterID: f.clusterID, DesiredRevision: placement.DesiredRevision,
	}
	if _, deleteErr := f.queries.DeleteAppliedPlacement(ctx, applied); deleteErr != nil {
		t.Fatal(deleteErr)
	}
	windows, err = placementWindows(t, client, token)
	if err != nil {
		t.Fatal(err)
	}
	if open, closed := windowSummary(windows); open != 0 || closed != 1 {
		t.Fatalf("after removal and deletion: %d open, %d closed windows, want one closed", open, closed)
	}
	removed := windows[0]
	if removed.GetRemovedAt().AsTime().Before(removed.GetPlacedAt().AsTime()) {
		t.Fatalf("window closes before it opens: %v", removed)
	}

	if _, deployErr := f.deploy(ctx, staticSpec); deployErr != nil {
		t.Fatal(deployErr)
	}
	windows, err = placementWindows(t, client, token)
	if err != nil {
		t.Fatal(err)
	}
	if open, closed := windowSummary(windows); open != 1 || closed != 1 {
		t.Fatalf("after placing again: %d open, %d closed windows", open, closed)
	}

	otherWindows, err := placementWindows(t, client, signedIngestToken(t, key, f.otherCluster, time.Now()))
	if err != nil || len(otherWindows) != 0 {
		t.Fatalf("other cluster windows = %v, %v, want none", otherWindows, err)
	}

	expired := signedIngestToken(t, key, f.clusterID, time.Now().Add(-time.Hour))
	_, err = placementWindows(t, client, expired)
	requireCode(t, err, connect.CodeUnauthenticated, "expired ingest token")
	_, err = placementWindows(t, client, testAssertion(t, f.clusterID))
	requireCode(t, err, connect.CodeUnauthenticated, "cluster assertion instead of an ingest token")

	if _, removeErr := f.pool.Exec(
		ctx,
		"UPDATE clusters SET removed_at = NOW() WHERE id = $1",
		f.clusterID,
	); removeErr != nil {
		t.Fatal(removeErr)
	}
	_, err = placementWindows(t, client, token)
	requireCode(t, err, connect.CodePermissionDenied, "removed cluster")
}

func TestIngestKeysUnavailableWhileTheSignerIs(t *testing.T) {
	f := newDeployFixture(t)
	key := newIngestKey(t, testCurrentIngestKey)
	signer := signingWith(key)
	server := newIngestServer(f, signer)
	token := signedIngestToken(t, key, f.clusterID, time.Now())
	signer.down = true
	_, err := placementWindows(t, ingestClient(server), token)
	requireCode(t, err, connect.CodeUnavailable, "windows while the signer is unavailable")
	recorder := httptest.NewRecorder()
	server.JWKS(recorder, httptest.NewRequestWithContext(t.Context(), http.MethodGet, ingesttoken.JWKSPath, nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("JWKS while the signer is unavailable = %d", recorder.Code)
	}
}
