package admin

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/team-loco/loco/cmd/internal/clitest"
	adminv1 "github.com/team-loco/loco/gen/go/loco/admin/v1"
	"github.com/team-loco/loco/gen/go/loco/admin/v1/adminv1connect"
)

type fakeAdmins struct {
	adminv1connect.UnimplementedSystemRoleServiceHandler
	t       *testing.T
	session string
	granted []*adminv1.GrantSystemRoleRequest
	revoked []string
	listed  *adminv1.ListSystemRolesResponse
}

func (f *fakeAdmins) checkSession(ctx context.Context) {
	info, ok := connect.CallInfoForServerContext(ctx)
	if !ok || info.RequestHeader().Get("Authorization") != "Bearer "+f.session {
		f.t.Error("CLI did not send the signed-in session")
	}
}

func (f *fakeAdmins) ListSystemRoles(
	ctx context.Context, _ *adminv1.ListSystemRolesRequest,
) (*adminv1.ListSystemRolesResponse, error) {
	f.checkSession(ctx)
	if f.listed != nil {
		return f.listed, nil
	}
	return listedRoles, nil
}

var listedRoles = &adminv1.ListSystemRolesResponse{
	Holders: []*adminv1.SystemRoleHolder{{
		UserId: "0192f3a0-0000-7000-8000-000000000001", Name: "Ada", Email: "ada@acme.test",
		LastActiveAt: timestamppb.New(time.Date(2026, 11, 20, 9, 30, 0, 0, time.UTC)),
		Issuer:       "https://idp.acme.test", Subject: "ada",
		ConfiguredRole: adminv1.SystemRole_SYSTEM_ROLE_READ, GrantedRole: adminv1.SystemRole_SYSTEM_ROLE_ADMIN,
	}},
	Unclaimed: []*adminv1.UnclaimedEmail{{Email: "grace@acme.test", Role: adminv1.SystemRole_SYSTEM_ROLE_READ}},
}

func (f *fakeAdmins) GrantSystemRole(
	ctx context.Context, req *adminv1.GrantSystemRoleRequest,
) (*adminv1.GrantSystemRoleResponse, error) {
	f.checkSession(ctx)
	f.granted = append(f.granted, req)
	return &adminv1.GrantSystemRoleResponse{}, nil
}

func (f *fakeAdmins) RevokeSystemRole(
	ctx context.Context, req *adminv1.RevokeSystemRoleRequest,
) (*adminv1.RevokeSystemRoleResponse, error) {
	f.checkSession(ctx)
	f.revoked = append(f.revoked, req.GetUserId())
	return &adminv1.RevokeSystemRoleResponse{}, nil
}

func runAdminCmd(t *testing.T, fake *fakeAdmins, args ...string) (string, error) {
	t.Helper()
	fake.t = t
	fake.session = clitest.Session(t, func(server *connect.Server) {
		adminv1connect.RegisterSystemRoleServiceHandler(server, fake)
	})
	cmd := BuildAdminCmd()
	output := new(bytes.Buffer)
	cmd.SetOut(output)
	cmd.SetErr(output)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return output.String(), err
}

func TestListPrintsHoldersWithNameAndLastActiveAndUnclaimedEmails(t *testing.T) {
	output, err := runAdminCmd(t, &fakeAdmins{}, "list")
	if err != nil {
		t.Fatalf("list: %v\n%s", err, output)
	}
	want := "USER ID                               NAME  EMAIL          CONFIGURED  " +
		"GRANTED  LAST ACTIVE          ISSUER                 SUBJECT\n" +
		"0192f3a0-0000-7000-8000-000000000001  Ada   ada@acme.test  read        " +
		"admin    2026-11-20 09:30:00  https://idp.acme.test  ada\n" +
		"\n" +
		"UNCLAIMED EMAIL  ROLE\n" +
		"grace@acme.test  read\n"
	if output != want {
		t.Fatalf("list output:\n%s\nwant:\n%s", output, want)
	}
}

func TestListShowsADashForAHolderWithoutSessionActivity(t *testing.T) {
	fake := &fakeAdmins{listed: &adminv1.ListSystemRolesResponse{Holders: []*adminv1.SystemRoleHolder{{
		UserId: "0192f3a0-0000-7000-8000-000000000002", Email: "grace@acme.test",
		Issuer: "https://idp.acme.test", Subject: "grace",
		ConfiguredRole: adminv1.SystemRole_SYSTEM_ROLE_ADMIN,
	}}}}
	output, err := runAdminCmd(t, fake, "list")
	if err != nil {
		t.Fatalf("list: %v\n%s", err, output)
	}
	want := "USER ID                               NAME  EMAIL            CONFIGURED  " +
		"GRANTED  LAST ACTIVE  ISSUER                 SUBJECT\n" +
		"0192f3a0-0000-7000-8000-000000000002        grace@acme.test  admin       " +
		"—        —            https://idp.acme.test  grace\n"
	if output != want {
		t.Fatalf("list output:\n%s\nwant:\n%s", output, want)
	}
}

func TestGrantSendsTheRoleAndRevokeTheUserID(t *testing.T) {
	userID := uuid.NewString()
	fake := &fakeAdmins{}
	if output, err := runAdminCmd(t, fake, "grant", userID); err != nil ||
		output != "Granted system admin to user "+userID+"\n" {
		t.Fatalf("grant: %v %q", err, output)
	}
	if output, err := runAdminCmd(t, fake, "grant", userID, "--role", "read"); err != nil ||
		output != "Granted system read to user "+userID+"\n" {
		t.Fatalf("grant read: %v %q", err, output)
	}
	if output, err := runAdminCmd(t, fake, "revoke", userID); err != nil ||
		output != "Revoked the granted system role of user "+userID+"\n" {
		t.Fatalf("revoke: %v %q", err, output)
	}
	want := []*adminv1.GrantSystemRoleRequest{
		{UserId: userID, Role: adminv1.SystemRole_SYSTEM_ROLE_ADMIN},
		{UserId: userID, Role: adminv1.SystemRole_SYSTEM_ROLE_READ},
	}
	equal := func(a, b *adminv1.GrantSystemRoleRequest) bool { return proto.Equal(a, b) }
	if !slices.EqualFunc(fake.granted, want, equal) {
		t.Fatalf("granted %v, want %v", fake.granted, want)
	}
	if len(fake.revoked) != 1 || fake.revoked[0] != userID {
		t.Fatalf("revoked %v, want %s once", fake.revoked, userID)
	}
}

func TestGrantRejectsAnUnknownRole(t *testing.T) {
	fake := &fakeAdmins{}
	if _, err := runAdminCmd(t, fake, "grant", uuid.NewString(), "--role", "write"); err == nil ||
		!strings.Contains(err.Error(), "invalid --role") {
		t.Fatalf("grant --role write err = %v, want an invalid role", err)
	}
	if len(fake.granted) != 0 {
		t.Fatalf("grant with an unknown role reached the API: %v", fake.granted)
	}
}

func TestGrantRejectsAnEmail(t *testing.T) {
	fake := &fakeAdmins{}
	if _, err := runAdminCmd(t, fake, "grant", "ada@acme.test"); err == nil ||
		!strings.Contains(err.Error(), "invalid user ID") {
		t.Fatalf("grant by email err = %v, want an invalid user ID", err)
	}
	if len(fake.granted) != 0 {
		t.Fatalf("grant by email reached the API: %v", fake.granted)
	}
}
