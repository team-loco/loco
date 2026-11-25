package user

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/team-loco/loco/cmd/internal/clitest"
	userv1 "github.com/team-loco/loco/gen/go/loco/user/v1"
	"github.com/team-loco/loco/gen/go/loco/user/v1/userv1connect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const targetEmail = "dev@acme.test"

type fakeUsers struct {
	userv1connect.UnimplementedUserServiceHandler
	t        *testing.T
	session  string
	requests []*userv1.SetUserStatusRequest
	users    []*userv1.User
	queries  []string
}

func (f *fakeUsers) ListUsers(
	_ context.Context,
	req *userv1.ListUsersRequest,
) (*userv1.ListUsersResponse, error) {
	f.queries = append(f.queries, req.GetQuery())
	// Two users per page, so the command has to follow the page token.
	start := 0
	if req.GetPageToken() != "" {
		start = 2
	}
	end := min(start+2, len(f.users))
	token := ""
	if end < len(f.users) {
		token = "next"
	}
	return &userv1.ListUsersResponse{Users: f.users[start:end], NextPageToken: token}, nil
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

func TestListShowsEveryPageWithStateAndLastActive(t *testing.T) {
	active := timestamppb.New(time.Date(2026, 11, 20, 9, 30, 0, 0, time.UTC))
	fake := &fakeUsers{users: []*userv1.User{
		{Name: "Ada", Email: "ada@acme.test", Status: userv1.UserStatus_USER_STATUS_ACTIVE, LastActiveAt: active},
		{Email: "grace@acme.test", Status: userv1.UserStatus_USER_STATUS_SUSPENDED},
		{Name: "Alan", Email: "alan@acme.test", Status: userv1.UserStatus_USER_STATUS_ACTIVE},
	}}
	output := runUserCmd(t, fake, "list", "--query", "acme")
	for _, want := range []string{
		"NAME", "LAST ACTIVE", "Ada", "ada@acme.test", "2026-11-20 09:30:00",
		"grace@acme.test", "suspended", "never", "Alan",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("output lacks %q:\n%s", want, output)
		}
	}
	if !slices.Equal(fake.queries, []string{"acme", "acme"}) {
		t.Errorf("queries = %v, want the query on both pages", fake.queries)
	}
}
