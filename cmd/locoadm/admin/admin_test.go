package admin

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

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
	return listedRoles, nil
}

var listedRoles = &adminv1.ListSystemRolesResponse{
	Holders: []*adminv1.SystemRoleHolder{{
		UserId: "0192f3a0-0000-7000-8000-000000000001", Email: "ada@acme.test",
		Issuer: "https://idp.acme.test", Subject: "ada",
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

func TestListPrintsRolesAndUnclaimedEmails(t *testing.T) {
	output, err := runAdminCmd(t, &fakeAdmins{}, "list")
	if err != nil {
		t.Fatalf("list: %v\n%s", err, output)
	}
	var got adminv1.ListSystemRolesResponse
	if err := protojson.Unmarshal([]byte(output), &got); err != nil {
		t.Fatalf("decode list output: %v\n%s", err, output)
	}
	if !proto.Equal(&got, listedRoles) {
		t.Fatalf("list output = %v, want %v", &got, listedRoles)
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
	if len(fake.granted) != len(want) || !proto.Equal(fake.granted[0], want[0]) ||
		!proto.Equal(fake.granted[1], want[1]) {
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
