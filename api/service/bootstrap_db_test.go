package service

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"connectrpc.com/validate"
	"github.com/google/uuid"
	"github.com/team-loco/loco/api/authz"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/interceptor"
	bootstrapv1 "github.com/team-loco/loco/gen/go/loco/bootstrap/v1"
	"github.com/team-loco/loco/gen/go/loco/bootstrap/v1/bootstrapv1connect"
	clusterv1 "github.com/team-loco/loco/gen/go/loco/cluster/v1"
	"github.com/team-loco/loco/internal/rpc"
)

const (
	testBootstrapToken  = "bootstrap-token-0123456789abcdef0123456789"
	wrongBootstrapToken = "bootstrap-token-fedcba9876543210fedcba9876"
	bootstrapContenders = 8
)

func bootstrapHTTP(t *testing.T, a *membershipAPI, tokenHash []byte) string {
	t.Helper()
	server := connect.NewServer(
		interceptor.NewErrorInterceptor(),
		interceptor.NewContextInterceptor().Interceptor(),
		interceptor.NewAuthInterceptor(a.machine, nil, nil, authz.New(a.pool, a.queries)).Interceptor(),
		validate.NewServerInterceptor(),
	)
	bootstrapv1connect.RegisterBootstrapServiceHandler(server, NewBootstrapServer(a.pool, tokenHash))
	mux := http.NewServeMux()
	connecthttp.Mount(mux, server)
	httpServer := httptest.NewServer(mux)
	t.Cleanup(httpServer.Close)
	return httpServer.URL
}

func bootstrapAs(
	t *testing.T,
	endpoint string,
	user membershipUser,
	token string,
) (*bootstrapv1.BootstrapResponse, error) {
	t.Helper()
	client := rpc.NewHTTPClients(http.DefaultClient, endpoint, user.token).Bootstrap
	return client.Bootstrap(t.Context(), &bootstrapv1.BootstrapRequest{Token: token})
}

func systemAdmins(t *testing.T, a *membershipAPI) []uuid.UUID {
	t.Helper()
	admins, err := a.queries.GetUsersWithScopeOnEntity(t.Context(), genDb.GetUsersWithScopeOnEntityParams{
		EntityType: genDb.EntityTypeSystem, EntityID: uuid.Nil, Scope: genDb.ScopeAdmin,
	})
	if err != nil {
		t.Fatal(err)
	}
	return admins
}

func requireBootstrapError(t *testing.T, err error, code connect.Code, want error) {
	t.Helper()
	connectErr, ok := errors.AsType[*connect.Error](err)
	if !ok {
		t.Fatalf("error = %v, want %v", err, want)
	}
	if connectErr.Code() != code || connectErr.Message() != want.Error() {
		t.Fatalf("error = %s %q, want %s %q", connectErr.Code(), connectErr.Message(), code, want)
	}
}

func TestBootstrapGrantsFirstSystemAdmin(t *testing.T) {
	a := newMembershipAPI(t)
	endpoint := bootstrapHTTP(t, a, HashBootstrapToken(testBootstrapToken))
	first := a.signUp(t, "first")
	response, err := bootstrapAs(t, endpoint, first, testBootstrapToken)
	if err != nil {
		t.Fatal(err)
	}
	if response.GetUserId() != first.id.String() {
		t.Fatalf("user id = %q, want %s", response.GetUserId(), first.id)
	}
	if admins := systemAdmins(t, a); len(admins) != 1 || admins[0] != first.id {
		t.Fatalf("system admins = %v, want [%s]", admins, first.id)
	}
	clusters := rpc.NewHTTPClients(http.DefaultClient, clusterAdminHTTP(t, a.pool, a.machine), first.token).ClusterAdmin
	if _, listErr := clusters.ListClusters(t.Context(), &clusterv1.ListClustersRequest{}); listErr != nil {
		t.Fatalf("bootstrapped admin cannot list clusters: %v", listErr)
	}
	second := a.signUp(t, "second")
	_, err = bootstrapAs(t, endpoint, second, testBootstrapToken)
	requireBootstrapError(t, err, connect.CodeAlreadyExists, ErrBootstrapClosed)
	if admins := systemAdmins(t, a); len(admins) != 1 {
		t.Fatalf("system admins = %v after a second bootstrap", admins)
	}
}

func TestBootstrapRejectsWrongToken(t *testing.T) {
	a := newMembershipAPI(t)
	endpoint := bootstrapHTTP(t, a, HashBootstrapToken(testBootstrapToken))
	user := a.signUp(t, "guesser")
	_, err := bootstrapAs(t, endpoint, user, wrongBootstrapToken)
	requireBootstrapError(t, err, connect.CodePermissionDenied, ErrBootstrapTokenInvalid)
	if admins := systemAdmins(t, a); len(admins) != 0 {
		t.Fatalf("wrong token granted %v", admins)
	}
	if _, grantErr := bootstrapAs(t, endpoint, user, testBootstrapToken); grantErr != nil {
		t.Fatalf("wrong token closed bootstrap: %v", grantErr)
	}
	_, err = bootstrapAs(t, endpoint, user, wrongBootstrapToken)
	requireBootstrapError(t, err, connect.CodePermissionDenied, ErrBootstrapTokenInvalid)
}

func TestBootstrapIsDisabledWithoutToken(t *testing.T) {
	a := newMembershipAPI(t)
	endpoint := bootstrapHTTP(t, a, nil)
	user := a.signUp(t, "early")
	_, err := bootstrapAs(t, endpoint, user, testBootstrapToken)
	requireBootstrapError(t, err, connect.CodeFailedPrecondition, ErrBootstrapDisabled)
	if admins := systemAdmins(t, a); len(admins) != 0 {
		t.Fatalf("disabled bootstrap granted %v", admins)
	}
}

func TestBootstrapRequiresSignedInUser(t *testing.T) {
	a := newMembershipAPI(t)
	endpoint := bootstrapHTTP(t, a, HashBootstrapToken(testBootstrapToken))
	_, err := bootstrapAs(t, endpoint, membershipUser{}, testBootstrapToken)
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("anonymous bootstrap: %v, want unauthenticated", err)
	}
}

func TestBootstrapRefusesWhenAdminExists(t *testing.T) {
	a := newMembershipAPI(t)
	existing := a.signUp(t, "existing")
	if err := a.queries.AddUserScope(t.Context(), genDb.AddUserScopeParams{
		UserID: existing.id, Scope: genDb.ScopeAdmin, EntityType: genDb.EntityTypeSystem, EntityID: uuid.Nil,
	}); err != nil {
		t.Fatal(err)
	}
	endpoint := bootstrapHTTP(t, a, HashBootstrapToken(testBootstrapToken))
	user := a.signUp(t, "latecomer")
	_, err := bootstrapAs(t, endpoint, user, testBootstrapToken)
	requireBootstrapError(t, err, connect.CodeAlreadyExists, ErrBootstrapClosed)
	if admins := systemAdmins(t, a); len(admins) != 1 || admins[0] != existing.id {
		t.Fatalf("system admins = %v, want [%s]", admins, existing.id)
	}
}

func TestBootstrapConcurrentAttemptsHaveOneWinner(t *testing.T) {
	a := newMembershipAPI(t)
	endpoint := bootstrapHTTP(t, a, HashBootstrapToken(testBootstrapToken))
	users := make([]membershipUser, bootstrapContenders)
	for i := range users {
		users[i] = a.signUp(t, uuid.NewString())
	}
	errs := make([]error, bootstrapContenders)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i, user := range users {
		wg.Go(func() {
			<-start
			_, errs[i] = bootstrapAs(t, endpoint, user, testBootstrapToken)
		})
	}
	close(start)
	wg.Wait()
	var winner uuid.UUID
	for i, err := range errs {
		if err == nil {
			if winner != uuid.Nil {
				t.Fatalf("two winners: %s and %s", winner, users[i].id)
			}
			winner = users[i].id
			continue
		}
		requireBootstrapError(t, err, connect.CodeAlreadyExists, ErrBootstrapClosed)
	}
	if admins := systemAdmins(t, a); len(admins) != 1 || admins[0] != winner {
		t.Fatalf("system admins = %v, want [%s]", admins, winner)
	}
}

func TestBootstrapStaysClosedAfterRestart(t *testing.T) {
	a := newMembershipAPI(t)
	tokenHash := HashBootstrapToken(testBootstrapToken)
	first := a.signUp(t, "first")
	if _, err := bootstrapAs(t, bootstrapHTTP(t, a, tokenHash), first, testBootstrapToken); err != nil {
		t.Fatal(err)
	}
	if err := a.queries.RemoveUserScope(t.Context(), genDb.RemoveUserScopeParams{
		UserID: first.id, Scope: genDb.ScopeAdmin, EntityType: genDb.EntityTypeSystem, EntityID: uuid.Nil,
	}); err != nil {
		t.Fatal(err)
	}
	restarted := bootstrapHTTP(t, a, tokenHash)
	second := a.signUp(t, "second")
	_, err := bootstrapAs(t, restarted, second, testBootstrapToken)
	requireBootstrapError(t, err, connect.CodeAlreadyExists, ErrBootstrapClosed)
	if admins := systemAdmins(t, a); len(admins) != 0 {
		t.Fatalf("restart reopened bootstrap: %v", admins)
	}
}
