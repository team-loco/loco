package interceptor

import (
	"net/http"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/team-loco/loco/api/auth/authtest"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/tvm"
	"github.com/team-loco/loco/gen/go/loco/auth/v1/authv1connect"
	"github.com/team-loco/loco/gen/go/loco/org/v1/orgv1connect"
)

func TestAuthenticateSkipsPublicProcedures(t *testing.T) {
	i := NewAuthInterceptor(nil, nil, nil, nil)
	ctx, err := i.authenticate(t.Context(), authv1connect.AuthServiceRefreshCLITokenProcedure, http.Header{})
	if err != nil {
		t.Fatalf("authenticate public procedure: %v", err)
	}
	if ctx != t.Context() {
		t.Fatal("authenticate changed the context of a public procedure")
	}
}

func TestAuthenticateRejectsMissingToken(t *testing.T) {
	i := NewAuthInterceptor(nil, nil, nil, nil)
	_, err := i.authenticate(t.Context(), orgv1connect.OrgServiceGetOrgProcedure, http.Header{})
	if code := connect.CodeOf(err); code != connect.CodeUnauthenticated {
		t.Fatalf("code = %v, want Unauthenticated", code)
	}
}

func TestAuthenticateReportsDatabaseFailure(t *testing.T) {
	pool := authtest.ClosedPool(t)
	machine := tvm.NewVendingMachine(pool, genDb.New(pool), tvm.Config{})
	t.Cleanup(machine.Close)
	i := NewAuthInterceptor(machine, nil, nil, nil)
	_, err := i.authenticate(t.Context(), orgv1connect.OrgServiceGetOrgProcedure, bearer("loco_s_unknown"))
	if err == nil || connect.CodeOf(err) == connect.CodeUnauthenticated {
		t.Fatalf("authenticate on a closed pool: got %v, want a database error that is not Unauthenticated", err)
	}
}
