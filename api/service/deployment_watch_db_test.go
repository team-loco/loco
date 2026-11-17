package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/google/uuid"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/interceptor"
	"github.com/team-loco/loco/api/pkg/pgnotify"
	"github.com/team-loco/loco/api/reqctx"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	"github.com/team-loco/loco/gen/go/loco/deployment/v1/deploymentv1connect"
	"github.com/team-loco/loco/internal/rpc"
)

func TestWatchDeploymentStreamCap(t *testing.T) {
	f := newDeployFixture(t)
	created, err := createDeployment(t, f, sameRegionSpec)
	if err != nil {
		t.Fatal(err)
	}
	server, ctx := f.deploymentWriter(t)
	server.notifier = pgnotify.New(f.pool.Config().ConnConfig, "deployment_activity", 30*time.Second)
	p, _ := reqctx.PrincipalFrom(ctx)
	p.Scopes = []genDb.EntityScope{
		{EntityType: genDb.EntityTypeResource, EntityID: f.resourceID, Scope: genDb.ScopeRead},
	}
	p.Entity = genDb.Entity{Type: genDb.EntityTypeUser, ID: uuid.New()}
	other := p
	other.Entity.ID = uuid.New()
	denied := p
	denied.Scopes = nil
	callers := map[string]reqctx.Principal{
		"Bearer first":     p,
		"Bearer second":    p,
		"Bearer other":     other,
		"Bearer no-access": denied,
	}
	finished := make(chan struct{}, 32)
	principals := func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
			defer func() { finished <- struct{}{} }()
			info, _ := connect.CallInfoForServerContext(ctx)
			return next(reqctx.WithPrincipal(ctx, callers[info.RequestHeader().Get("Authorization")]), spec, stream)
		}
	}
	rpcServer := connect.NewServer(interceptor.NewErrorInterceptor(), principals)
	deploymentv1connect.RegisterDeploymentServiceHandler(rpcServer, server)
	mux := http.NewServeMux()
	connecthttp.Mount(mux, rpcServer)
	endpoint := httptest.NewServer(mux)
	t.Cleanup(endpoint.Close)
	request := &deploymentv1.WatchDeploymentRequest{DeploymentId: created.GetDeploymentId()}
	open := func(
		token string,
		request *deploymentv1.WatchDeploymentRequest,
	) (context.CancelFunc, deploymentv1connect.DeploymentServiceWatchDeploymentClientStream) {
		t.Helper()
		ctx, cancel := context.WithCancel(t.Context())
		stream, openErr := rpc.ForHost(endpoint.URL, token).Deployment.WatchDeployment(ctx, request)
		if openErr != nil {
			cancel()
			t.Fatal(openErr)
		}
		t.Cleanup(func() {
			cancel()
			if closeErr := stream.Close(); closeErr != nil {
				t.Error(closeErr)
			}
		})
		return cancel, stream
	}
	for _, test := range []struct {
		token string
		id    string
		code  connect.Code
	}{
		{"first", uuid.NewString(), connect.CodeNotFound},
		{"no-access", request.GetDeploymentId(), connect.CodeNotFound},
	} {
		for range 9 {
			_, stream := open(test.token, &deploymentv1.WatchDeploymentRequest{DeploymentId: test.id})
			if _, recvErr := stream.Receive(); connect.CodeOf(recvErr) != test.code {
				t.Fatalf("stream error = %v, want %v", recvErr, test.code)
			}
			<-finished
		}
	}
	var cancelFirst context.CancelFunc
	for range 8 {
		cancel, stream := open("first", request)
		if _, recvErr := stream.Receive(); recvErr != nil {
			t.Fatal(recvErr)
		}
		cancelFirst = cancel
	}
	_, excess := open("second", request)
	if _, recvErr := excess.Receive(); connect.CodeOf(recvErr) != connect.CodeResourceExhausted {
		t.Fatalf("excess stream error = %v, want resource_exhausted", recvErr)
	}
	<-finished
	cancelOther, separate := open("other", request)
	if _, recvErr := separate.Receive(); recvErr != nil {
		t.Fatalf("other principal: %v", recvErr)
	}
	cancelOther()
	<-finished
	cancelFirst()
	<-finished
	_, reopened := open("second", request)
	if _, recvErr := reopened.Receive(); recvErr != nil {
		t.Fatalf("released slot: %v", recvErr)
	}
}

func TestDeploymentWatchConcurrentAdmission(t *testing.T) {
	s := &DeploymentServer{watchLimit: 3, watchStreams: make(map[genDb.Entity]int32)}
	entity := genDb.Entity{Type: genDb.EntityTypeUser, ID: uuid.New()}
	var admitted atomic.Int32
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			if s.acquireWatch(entity) {
				admitted.Add(1)
			}
		})
	}
	wg.Wait()
	if got := admitted.Load(); got != s.watchLimit {
		t.Fatalf("admitted = %d, want %d", got, s.watchLimit)
	}
	for range admitted.Load() {
		s.releaseWatch(entity)
	}
	if len(s.watchStreams) != 0 {
		t.Fatalf("finished principals = %v", s.watchStreams)
	}
	if !s.acquireWatch(entity) {
		t.Fatal("released budget rejected a stream")
	}
	s.releaseWatch(entity)
}
