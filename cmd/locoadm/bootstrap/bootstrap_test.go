package bootstrap

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/internal/keychain"
	bootstrapv1 "github.com/team-loco/loco/gen/go/loco/bootstrap/v1"
	"github.com/team-loco/loco/gen/go/loco/bootstrap/v1/bootstrapv1connect"
	"github.com/team-loco/loco/internal/bearer"
	"github.com/team-loco/loco/internal/rpc"
	"github.com/team-loco/loco/internal/rpcerr"
)

var errTestClosed = errors.New("closed")

type fakeBootstrap struct {
	bootstrapv1connect.UnimplementedBootstrapServiceHandler
	t       *testing.T
	session string
	userID  string
	tokens  []string
	err     error
}

func newFakeBootstrap(t *testing.T, err error) *fakeBootstrap {
	t.Helper()
	return &fakeBootstrap{t: t, session: bearer.New(""), userID: uuid.NewString(), err: err}
}

func (f *fakeBootstrap) Bootstrap(
	ctx context.Context,
	req *bootstrapv1.BootstrapRequest,
) (*bootstrapv1.BootstrapResponse, error) {
	info, ok := connect.CallInfoForServerContext(ctx)
	if !ok || info.RequestHeader().Get("Authorization") != "Bearer "+f.session {
		f.t.Error("CLI did not send the signed-in session")
	}
	f.tokens = append(f.tokens, req.GetToken())
	if f.err != nil {
		return nil, f.err
	}
	return &bootstrapv1.BootstrapResponse{UserId: f.userID}, nil
}

func bootstrapTestCommand(t *testing.T, fake *fakeBootstrap) (*cobra.Command, *bytes.Buffer) {
	t.Helper()
	server := connect.NewServer()
	bootstrapv1connect.RegisterBootstrapServiceHandler(server, fake)
	mux := http.NewServeMux()
	connecthttp.Mount(mux, server)
	endpoint := httptest.NewServer(mux)
	t.Cleanup(endpoint.Close)
	t.Setenv("LOCO_HOST", endpoint.URL)
	cmd := newBootstrapCmd(dependencies{
		token: func(*cobra.Command) (*keychain.UserToken, error) {
			return &keychain.UserToken{Host: endpoint.URL, Token: fake.session}, nil
		},
		client: func(host, token string) bootstrapv1connect.BootstrapServiceClient {
			return rpc.NewHTTPClients(endpoint.Client(), host, token).Bootstrap
		},
	})
	output := new(bytes.Buffer)
	cmd.SetOut(output)
	cmd.SetErr(output)
	return cmd, output
}

func TestBootstrapSendsTokenAsSignedInUser(t *testing.T) {
	fake := newFakeBootstrap(t, nil)
	cmd, output := bootstrapTestCommand(t, fake)
	testBootstrapToken := bearer.New("")
	cmd.SetArgs([]string{"--token", testBootstrapToken})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if len(fake.tokens) != 1 || fake.tokens[0] != testBootstrapToken {
		t.Fatalf("tokens = %v", fake.tokens)
	}
	if !strings.Contains(output.String(), fake.userID) {
		t.Fatalf("output = %q", output)
	}
}

func TestBootstrapRequiresToken(t *testing.T) {
	fake := newFakeBootstrap(t, nil)
	cmd, _ := bootstrapTestCommand(t, fake)
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err == nil {
		t.Fatal("accepted a missing token")
	}
	if len(fake.tokens) != 0 {
		t.Fatalf("made RPC without a token: %v", fake.tokens)
	}
}

func TestBootstrapReturnsServerError(t *testing.T) {
	fake := newFakeBootstrap(t, rpcerr.New(connect.CodeAlreadyExists, errTestClosed))
	cmd, _ := bootstrapTestCommand(t, fake)
	cmd.SetArgs([]string{"--token", bearer.New("")})
	err := cmd.Execute()
	if connect.CodeOf(err) != connect.CodeAlreadyExists || !strings.Contains(err.Error(), errTestClosed.Error()) {
		t.Fatalf("error = %v", err)
	}
}
