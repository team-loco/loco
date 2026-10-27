package user

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/internal/keychain"
	userv1 "github.com/team-loco/loco/gen/go/loco/user/v1"
	"github.com/team-loco/loco/gen/go/loco/user/v1/userv1connect"
	"github.com/team-loco/loco/internal/bearer"
	"github.com/team-loco/loco/internal/rpc"
)

const targetEmail = "dev@acme.test"

type fakeUsers struct {
	userv1connect.UnimplementedUserServiceHandler
	t        *testing.T
	session  string
	requests []*userv1.SetUserStatusRequest
}

func (f *fakeUsers) SetUserStatus(
	ctx context.Context,
	req *userv1.SetUserStatusRequest,
) (*userv1.SetUserStatusResponse, error) {
	info, ok := connect.CallInfoForServerContext(ctx)
	if !ok || info.RequestHeader().Get("Authorization") != "Bearer "+f.session {
		f.t.Error("CLI did not send the signed-in session")
	}
	f.requests = append(f.requests, req)
	return &userv1.SetUserStatusResponse{}, nil
}

func runUserCmd(t *testing.T, fake *fakeUsers, args ...string) string {
	t.Helper()
	fake.t = t
	fake.session = bearer.New("")
	server := connect.NewServer()
	userv1connect.RegisterUserServiceHandler(server, fake)
	mux := http.NewServeMux()
	connecthttp.Mount(mux, server)
	endpoint := httptest.NewServer(mux)
	t.Cleanup(endpoint.Close)
	t.Setenv("LOCO_HOST", endpoint.URL)
	cmd := newUserCmd(dependencies{
		token: func(*cobra.Command) (*keychain.UserToken, error) {
			return &keychain.UserToken{Host: endpoint.URL, Token: fake.session}, nil
		},
		client: func(host, token string) userv1connect.UserServiceClient {
			return rpc.NewHTTPClients(endpoint.Client(), host, token).User
		},
	})
	output := new(bytes.Buffer)
	cmd.SetOut(output)
	cmd.SetErr(output)
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("%v: %v\n%s", args, err, output)
	}
	return output.String()
}

func TestSuspendAndReinstateSendTheEmailAndStatus(t *testing.T) {
	cases := []struct {
		operation string
		status    userv1.UserStatus
		output    string
	}{
		{userSuspend, userv1.UserStatus_USER_STATUS_SUSPENDED, "User " + targetEmail + ": suspended\n"},
		{userReinstate, userv1.UserStatus_USER_STATUS_ACTIVE, "User " + targetEmail + ": active\n"},
	}
	for _, tc := range cases {
		t.Run(tc.operation, func(t *testing.T) {
			fake := &fakeUsers{}
			output := runUserCmd(t, fake, tc.operation, targetEmail)
			if output != tc.output {
				t.Fatalf("output = %q, want %q", output, tc.output)
			}
			if len(fake.requests) != 1 ||
				fake.requests[0].GetEmail() != targetEmail ||
				fake.requests[0].GetStatus() != tc.status {
				t.Fatalf("requests = %v", fake.requests)
			}
		})
	}
}
