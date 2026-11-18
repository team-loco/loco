package user

import (
	"bytes"
	"context"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/team-loco/loco/cmd/internal/clitest"
	userv1 "github.com/team-loco/loco/gen/go/loco/user/v1"
	"github.com/team-loco/loco/gen/go/loco/user/v1/userv1connect"
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
	fake.session = clitest.Session(t, func(server *connect.Server) {
		userv1connect.RegisterUserServiceHandler(server, fake)
	})
	cmd := BuildUserCmd()
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
