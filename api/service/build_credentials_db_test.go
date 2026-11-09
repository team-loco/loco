package service

import (
	"context"
	"net/http"
	"reflect"
	"testing"

	"github.com/google/uuid"

	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/imagepath"
	"github.com/team-loco/loco/api/pkg/registrytoken"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	buildv1 "github.com/team-loco/loco/gen/go/loco/build/v1"
)

type hookedBuildCredential struct {
	genDb.Querier
	before func() error
}

func (q *hookedBuildCredential) CreateBuildRegistryCredential(
	ctx context.Context,
	arg genDb.CreateBuildRegistryCredentialParams,
) (int64, error) {
	if err := q.before(); err != nil {
		return 0, err
	}
	return q.Querier.CreateBuildRegistryCredential(ctx, arg)
}

func (f *buildFixture) tokenModeServer(queries genDb.Querier) *AgentServer {
	config := testAgentConfig()
	config.RegistryTokenMode = true
	return NewAgentServer(f.pool, queries, nil, f.bucket, nil, testHeartbeatInterval, config)
}

func (f *buildFixture) sendBuilds(t *testing.T, session *syncSession, sent *sentMessages) []*agentv1.StartBuild {
	t.Helper()
	if err := session.sendPending(f.ctx); err != nil {
		t.Fatalf("send pending: %v", err)
	}
	messages := sent.take()
	return startedBuilds(messages)
}

func (f *buildFixture) buildCredentialCount(t *testing.T, id string) int {
	t.Helper()
	var n int
	row := f.pool.QueryRow(f.ctx, "SELECT count(*) FROM registry_credentials WHERE build_id = $1", id)
	if err := row.Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func onlyStart(t *testing.T, starts []*agentv1.StartBuild, id string) *agentv1.StartBuild {
	t.Helper()
	if len(starts) != 1 || starts[0].GetBuildId() != id {
		t.Fatalf("started %v, want only %s", starts, id)
	}
	return starts[0]
}

func buildRepositoryPath(t *testing.T, start *agentv1.StartBuild) string {
	t.Helper()
	repository := start.GetImageRepository()
	path, err := imagepath.Path(testRegistryHost, repository)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func wantBuildAccess(t *testing.T, realm *registryFixture, credential, repository string) {
	t.Helper()
	other := workspaceRepository(realm.workspaceID.String(), uuid.NewString())
	want := []registrytoken.Access{{
		Type:    registrytoken.TypeRepository,
		Name:    repository,
		Actions: []string{registrytoken.ActionPull, registrytoken.ActionPush},
	}}
	for _, method := range registryExchanges {
		own := "repository:" + repository + ":pull,push"
		foreign := "repository:" + other + ":pull,push"
		exchange := buildExchange(method, credential, own, foreign)
		if access := realm.access(t, exchange); !reflect.DeepEqual(access, want) {
			t.Fatalf("%s access = %+v, want %+v", method, access, want)
		}
	}
}

func wantBuildRefused(t *testing.T, realm *registryFixture, credential, repository, what string) {
	t.Helper()
	for _, method := range registryExchanges {
		exchange := buildExchange(method, credential, "repository:"+repository+":pull,push")
		recorder := realm.exchange(t, exchange)
		wantRegistryStatus(t, recorder, http.StatusUnauthorized, method+" "+what)
	}
}

func TestTokenModeBuildCredentialGrantsItsRepositoryUntilTheBuildFinishes(t *testing.T) {
	f := newBuildFixture(t)
	realm := registryFixtureFor(t, f.deployFixture, testRegistryConfig())
	server := f.tokenModeServer(f.queries)
	sent := &sentMessages{}
	id := f.queued(t)

	first := newSyncSession(server, f.clusterID, sent.send)
	firstStart := onlyStart(t, f.sendBuilds(t, first, sent), id)
	firstCredential := firstStart.GetRegistryCredential()
	if firstCredential == "" {
		t.Fatal("StartBuild carries no registry credential in token mode")
	}
	repository := buildRepositoryPath(t, firstStart)
	wantBuildAccess(t, realm, firstCredential, repository)

	reconnected := newSyncSession(server, f.clusterID, sent.send)
	resent := onlyStart(t, f.sendBuilds(t, reconnected, sent), id)
	secondCredential := resent.GetRegistryCredential()
	if secondCredential == "" || secondCredential == firstCredential {
		t.Fatalf("resent credential %q, want a new one", secondCredential)
	}
	if n := f.buildCredentialCount(t, id); n != 2 {
		t.Fatalf("%d build credentials stored, want 2", n)
	}
	wantBuildAccess(t, realm, firstCredential, repository)
	wantBuildAccess(t, realm, secondCredential, repository)

	running := buildStatus(id, agentv1.BuildPhase_BUILD_PHASE_RUNNING)
	if err := reconnected.handleBuildStatus(f.ctx, running); err != nil {
		t.Fatalf("running: %v", err)
	}
	wantBuildAccess(t, realm, firstCredential, repository)
	wantBuildAccess(t, realm, secondCredential, repository)

	realm.exec(t, "UPDATE builds SET cluster_id = $2 WHERE id = $1", id, f.otherCluster)
	wantBuildRefused(t, realm, firstCredential, repository, "first credential after the build moved cluster")
	wantBuildRefused(t, realm, secondCredential, repository, "second credential after the build moved cluster")
	realm.exec(t, "UPDATE builds SET cluster_id = $2 WHERE id = $1", id, f.clusterID)

	succeeded := buildStatus(id, agentv1.BuildPhase_BUILD_PHASE_SUCCEEDED)
	succeeded.ImageDigest = testImageDigest
	if err := reconnected.handleBuildStatus(f.ctx, succeeded); err != nil {
		t.Fatalf("succeeded: %v", err)
	}
	wantStatus(t, f.get(t, id), buildv1.BuildStatus_BUILD_STATUS_SUCCEEDED)
	wantBuildRefused(t, realm, firstCredential, repository, "first credential after the build finished")
	wantBuildRefused(t, realm, secondCredential, repository, "second credential after the build finished")
}

func TestStaticModeStartBuildCarriesNoRegistryCredential(t *testing.T) {
	f := newBuildFixture(t)
	session, sent := f.dispatchSession()
	id := f.queued(t)

	start := onlyStart(t, f.sendBuilds(t, session, sent), id)
	if credential := start.GetRegistryCredential(); credential != "" {
		t.Fatalf("static mode StartBuild carries registry credential %q", credential)
	}
	if n := f.buildCredentialCount(t, id); n != 0 {
		t.Fatalf("%d build credentials stored in static mode, want 0", n)
	}
}

func TestBuildCredentialWriteFailureKeepsTheBuildQueued(t *testing.T) {
	f := newBuildFixture(t)
	realm := registryFixtureFor(t, f.deployFixture, testRegistryConfig())
	failures := 1
	flaky := &hookedBuildCredential{Querier: f.queries, before: func() error {
		if failures == 0 {
			return nil
		}
		failures--
		return errInjectedWrite
	}}
	server := f.tokenModeServer(flaky)
	sent := &sentMessages{}
	session := newSyncSession(server, f.clusterID, sent.send)
	id := f.queued(t)

	if starts := f.sendBuilds(t, session, sent); len(starts) != 0 {
		t.Fatalf("started %v after the credential write failed", starts)
	}
	wantStatus(t, f.get(t, id), buildv1.BuildStatus_BUILD_STATUS_QUEUED)
	if n := f.buildCredentialCount(t, id); n != 0 {
		t.Fatalf("%d build credentials stored after a failed write, want 0", n)
	}

	start := onlyStart(t, f.sendBuilds(t, session, sent), id)
	repository := buildRepositoryPath(t, start)
	wantBuildAccess(t, realm, start.GetRegistryCredential(), repository)
}

func TestBuildThatLeavesTheQueueBeforeItsCredentialIsNotSent(t *testing.T) {
	f := newBuildFixture(t)
	id := f.queued(t)
	canceling := &hookedBuildCredential{Querier: f.queries, before: func() error {
		_, err := f.cancel(id)
		return err
	}}
	server := f.tokenModeServer(canceling)
	sent := &sentMessages{}
	session := newSyncSession(server, f.clusterID, sent.send)

	if err := session.sendPending(f.ctx); err != nil {
		t.Fatalf("send pending: %v", err)
	}
	if msgs := sent.take(); len(msgs) != 0 {
		t.Fatalf("sent %v for a build canceled before its credential was stored", msgs)
	}
	if n := f.buildCredentialCount(t, id); n != 0 {
		t.Fatalf("%d build credentials stored for a canceled build, want 0", n)
	}
}
