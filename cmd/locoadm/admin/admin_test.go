package admin

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/google/uuid"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/team-loco/loco/cmd/internal/keychain"
	adminv1 "github.com/team-loco/loco/gen/go/loco/admin/v1"
	"github.com/team-loco/loco/gen/go/loco/admin/v1/adminv1connect"
	"github.com/team-loco/loco/internal/bearer"
)

type fakeAdmins struct {
	adminv1connect.UnimplementedSystemAdminServiceHandler
	t       *testing.T
	session string
	granted []string
	revoked []string
}

func (f *fakeAdmins) checkSession(ctx context.Context) {
	info, ok := connect.CallInfoForServerContext(ctx)
	if !ok || info.RequestHeader().Get("Authorization") != "Bearer "+f.session {
		f.t.Error("CLI did not send the signed-in session")
	}
}

func (f *fakeAdmins) ListSystemAdmins(
	ctx context.Context, _ *adminv1.ListSystemAdminsRequest,
) (*adminv1.ListSystemAdminsResponse, error) {
	f.checkSession(ctx)
	return listedAdmins, nil
}

var listedAdmins = &adminv1.ListSystemAdminsResponse{
	Admins: []*adminv1.SystemAdmin{{
		UserId: "0192f3a0-0000-7000-8000-000000000001", Email: "ada@acme.test",
		Issuer: "https://idp.acme.test", Subject: "ada",
		Sources: []adminv1.SystemAdminSource{adminv1.SystemAdminSource_SYSTEM_ADMIN_SOURCE_CONFIG},
	}},
	UnclaimedEmails: []string{"grace@acme.test"},
}

func (f *fakeAdmins) GrantSystemAdmin(
	ctx context.Context, req *adminv1.GrantSystemAdminRequest,
) (*adminv1.GrantSystemAdminResponse, error) {
	f.checkSession(ctx)
	f.granted = append(f.granted, req.GetUserId())
	return &adminv1.GrantSystemAdminResponse{}, nil
}

func (f *fakeAdmins) RevokeSystemAdmin(
	ctx context.Context, req *adminv1.RevokeSystemAdminRequest,
) (*adminv1.RevokeSystemAdminResponse, error) {
	f.checkSession(ctx)
	f.revoked = append(f.revoked, req.GetUserId())
	return &adminv1.RevokeSystemAdminResponse{}, nil
}

func runAdminCmd(t *testing.T, fake *fakeAdmins, args ...string) (string, error) {
	t.Helper()
	fake.t = t
	fake.session = bearer.New("")
	server := connect.NewServer()
	adminv1connect.RegisterSystemAdminServiceHandler(server, fake)
	mux := http.NewServeMux()
	connecthttp.Mount(mux, server)
	endpoint := httptest.NewServer(mux)
	t.Cleanup(endpoint.Close)
	t.Setenv("LOCO_HOST", endpoint.URL)
	t.Setenv("HOME", t.TempDir())
	t.Setenv(keychain.StoreEnvVar, "file")
	store, err := keychain.ForCurrentUser()
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Set(keychain.UserToken{
		Host: endpoint.URL, Token: fake.session, ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	cmd := BuildAdminCmd()
	output := new(bytes.Buffer)
	cmd.SetOut(output)
	cmd.SetErr(output)
	cmd.SetArgs(args)
	err = cmd.Execute()
	return output.String(), err
}

func TestListPrintsAdminsAndUnclaimedEmails(t *testing.T) {
	output, err := runAdminCmd(t, &fakeAdmins{}, "list")
	if err != nil {
		t.Fatalf("list: %v\n%s", err, output)
	}
	var got adminv1.ListSystemAdminsResponse
	if err := protojson.Unmarshal([]byte(output), &got); err != nil {
		t.Fatalf("decode list output: %v\n%s", err, output)
	}
	if !proto.Equal(&got, listedAdmins) {
		t.Fatalf("list output = %v, want %v", &got, listedAdmins)
	}
}

func TestGrantAndRevokeSendTheUserID(t *testing.T) {
	userID := uuid.NewString()
	fake := &fakeAdmins{}
	if output, err := runAdminCmd(t, fake, "grant", userID); err != nil ||
		output != "System admin granted for user "+userID+"\n" {
		t.Fatalf("grant: %v %q", err, output)
	}
	if output, err := runAdminCmd(t, fake, "revoke", userID); err != nil ||
		output != "System admin revoked for user "+userID+"\n" {
		t.Fatalf("revoke: %v %q", err, output)
	}
	if len(fake.granted) != 1 || fake.granted[0] != userID || len(fake.revoked) != 1 || fake.revoked[0] != userID {
		t.Fatalf("granted %v, revoked %v; want %s once each", fake.granted, fake.revoked, userID)
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
