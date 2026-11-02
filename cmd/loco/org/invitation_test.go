package org

import (
	"bytes"
	"context"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/internal/keychain"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
	"github.com/team-loco/loco/gen/go/loco/org/v1/orgv1connect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	statusEmailed         = "emailed"
	testInvitationOrgID   = "00000000-0000-7000-8000-0000000000a1"
	testInvitationOrgName = "acme"
	testInvitationID      = "00000000-0000-7000-8000-0000000000b1"
	testInviteeEmail      = "bob@acme.test"
	testInviterEmail      = "owner@acme.test"
	testInviteLink        = "https://app.loco.test/login?login_hint=bob%40acme.test"
)

var testInvitationExpiry = time.Date(2030, time.March, 4, 15, 30, 0, 0, time.Local)

type invitationServer struct {
	orgv1connect.UnimplementedOrgServiceHandler
	t          *testing.T
	mu         sync.Mutex
	credential string
	status     orgv1.InvitationStatus
	failure    string
	pages      [][]*orgv1.Invitation
	listCalls  int
	revoked    []string
	invited    []string
}

func (s *invitationServer) authenticated(ctx context.Context) {
	s.t.Helper()
	info, ok := connect.CallInfoForServerContext(ctx)
	if !ok || info.RequestHeader().Get("Authorization") != "Bearer "+s.credential {
		s.t.Error("invitation request is not authenticated")
	}
}

func (s *invitationServer) GetOrg(ctx context.Context, req *orgv1.GetOrgRequest) (*orgv1.GetOrgResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.authenticated(ctx)
	if req.GetOrgName() != testInvitationOrgName {
		s.t.Errorf("org name = %q", req.GetOrgName())
	}
	return &orgv1.GetOrgResponse{Organization: &orgv1.Organization{Id: testInvitationOrgID}}, nil
}

func (s *invitationServer) InviteOrgMember(
	ctx context.Context,
	req *orgv1.InviteOrgMemberRequest,
) (*orgv1.InviteOrgMemberResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.authenticated(ctx)
	if req.GetOrgId() != testInvitationOrgID {
		s.t.Errorf("org id = %q", req.GetOrgId())
	}
	s.invited = append(s.invited, req.GetEmail())
	return &orgv1.InviteOrgMemberResponse{Invitation: &orgv1.Invitation{
		Id:            testInvitationID,
		Email:         req.GetEmail(),
		Status:        s.status,
		DeliveryError: s.failure,
		Link:          testInviteLink,
	}}, nil
}

func (s *invitationServer) ListInvitations(
	ctx context.Context,
	req *orgv1.ListInvitationsRequest,
) (*orgv1.ListInvitationsResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.authenticated(ctx)
	if req.GetOrgId() != testInvitationOrgID {
		s.t.Errorf("org id = %q", req.GetOrgId())
	}
	page := s.pages[s.listCalls]
	s.listCalls++
	next := ""
	if s.listCalls < len(s.pages) {
		next = "page-" + string(rune('0'+s.listCalls))
	}
	return &orgv1.ListInvitationsResponse{Invitations: page, NextPageToken: next}, nil
}

func (s *invitationServer) RevokeInvitation(
	ctx context.Context,
	req *orgv1.RevokeInvitationRequest,
) (*orgv1.RevokeInvitationResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.authenticated(ctx)
	s.revoked = append(s.revoked, req.GetId())
	return &orgv1.RevokeInvitationResponse{}, nil
}

func runInvitationCmd(
	t *testing.T,
	fake *invitationServer,
	build func(invitationDeps) *cobra.Command,
	args ...string,
) (string, error) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv(keychain.StoreEnvVar, "file")
	t.Setenv("LOCO_ORG", testInvitationOrgName)
	fake.t = t
	fake.credential = rand.Text()
	server := connect.NewServer()
	orgv1connect.RegisterOrgServiceHandler(server, fake)
	mux := http.NewServeMux()
	connecthttp.Mount(mux, server)
	endpoint := httptest.NewServer(mux)
	t.Cleanup(endpoint.Close)
	t.Setenv("LOCO_HOST", endpoint.URL)
	store, err := keychain.ForCurrentUser()
	if err != nil {
		t.Fatal(err)
	}
	token := keychain.UserToken{Host: endpoint.URL, Token: fake.credential, ExpiresAt: time.Now().Add(time.Hour)}
	err = store.Set(token)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	cmd := build(invitationDeps{NewOrgClient: realOrgClient, Output: &output})
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	cmd.SetArgs(args)
	err = cmd.Execute()
	return output.String(), err
}

func TestInviteReportsDeliveryStatus(t *testing.T) {
	cases := []struct {
		name    string
		status  orgv1.InvitationStatus
		failure string
		want    string
	}{
		{"emailed", orgv1.InvitationStatus_INVITATION_STATUS_EMAILED, "", statusEmailed},
		{"failed", orgv1.InvitationStatus_INVITATION_STATUS_FAILED, "relay refused", "email failed: relay refused"},
		{"no transport", orgv1.InvitationStatus_INVITATION_STATUS_NO_TRANSPORT, "", "no email transport configured"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := &invitationServer{status: tc.status, failure: tc.failure}
			output, err := runInvitationCmd(t, fake, newInviteCmd, testInviteeEmail)
			if err != nil {
				t.Fatal(err)
			}
			want := "Invited " + testInviteeEmail + ": " + tc.want + "\nLink: " + testInviteLink + "\n"
			if output != want {
				t.Fatalf("output = %q, want %q", output, want)
			}
			if len(fake.invited) != 1 || fake.invited[0] != testInviteeEmail {
				t.Fatalf("invited = %v", fake.invited)
			}
		})
	}
}

func TestInvitationsListsEveryPage(t *testing.T) {
	invitation := func(id, email string, status orgv1.InvitationStatus) *orgv1.Invitation {
		return &orgv1.Invitation{
			Id:        id,
			Email:     email,
			InvitedBy: &orgv1.User{Email: testInviterEmail},
			ExpiresAt: timestamppb.New(testInvitationExpiry),
			Status:    status,
		}
	}
	fake := &invitationServer{pages: [][]*orgv1.Invitation{
		{invitation("id-1", "a@acme.test", orgv1.InvitationStatus_INVITATION_STATUS_EMAILED)},
		{
			invitation("id-2", "b@acme.test", orgv1.InvitationStatus_INVITATION_STATUS_NO_TRANSPORT),
			invitation("id-3", "c@acme.test", orgv1.InvitationStatus_INVITATION_STATUS_EXPIRED),
		},
	}}
	output, err := runInvitationCmd(t, fake, newInvitationsCmd)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) != 4 {
		t.Fatalf("output = %q", output)
	}
	expires := testInvitationExpiry.Format(invitationTimeLayout)
	for i, want := range [][]string{
		{"ID", "EMAIL", "INVITED BY", "EXPIRES", "STATUS"},
		{"id-1", "a@acme.test", testInviterEmail, expires, "emailed"},
		{"id-2", "b@acme.test", testInviterEmail, expires, "no email transport configured"},
		{"id-3", "c@acme.test", testInviterEmail, expires, "expired"},
	} {
		if got := strings.Join(strings.Fields(lines[i]), " "); got != strings.Join(want, " ") {
			t.Errorf("line %d = %q, want %q", i, got, strings.Join(want, " "))
		}
	}
}

func TestInvitationsEmpty(t *testing.T) {
	fake := &invitationServer{pages: [][]*orgv1.Invitation{nil}}
	output, err := runInvitationCmd(t, fake, newInvitationsCmd)
	if err != nil || output != "No pending invitations.\n" {
		t.Fatalf("output = %q, err = %v", output, err)
	}
}

func TestRevokeInviteSendsTheID(t *testing.T) {
	fake := &invitationServer{}
	output, err := runInvitationCmd(t, fake, newRevokeInviteCmd, testInvitationID)
	if err != nil {
		t.Fatal(err)
	}
	if output != "Invitation "+testInvitationID+" revoked.\n" {
		t.Fatalf("output = %q", output)
	}
	if len(fake.revoked) != 1 || fake.revoked[0] != testInvitationID {
		t.Fatalf("revoked = %v", fake.revoked)
	}
}
