package org

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
	"github.com/team-loco/loco/gen/go/loco/org/v1/orgv1connect"
	userv1 "github.com/team-loco/loco/gen/go/loco/user/v1"
	"github.com/team-loco/loco/gen/go/loco/user/v1/userv1connect"
	"github.com/team-loco/loco/internal/keychain"
	"github.com/team-loco/loco/internal/rpc"
)

const (
	testListUserID   = "00000000-0000-7000-8000-000000000001"
	testFirstOrgID   = "00000000-0000-7000-8000-000000000002"
	testLastOrgID    = "00000000-0000-7000-8000-000000000003"
	testFirstOrgName = "First"
)

var errOrganizationListUnavailable = errors.New("organization listing unavailable")

type organizationPage struct {
	id   string
	name string
	next string
	err  error
}

type organizationPagesServer struct {
	orgv1connect.UnimplementedOrgServiceHandler
	userv1connect.UnimplementedUserServiceHandler
	t          *testing.T
	mu         sync.Mutex
	credential string
	pages      []organizationPage
	calls      int
	whoCalls   int
}

func (s *organizationPagesServer) authenticated(ctx context.Context) {
	s.t.Helper()
	info, ok := connect.CallInfoForServerContext(ctx)
	if !ok || info.RequestHeader().Get("Authorization") != "Bearer "+s.credential {
		s.t.Error("organization request is not authenticated")
	}
}

func (s *organizationPagesServer) WhoAmI(ctx context.Context, _ *userv1.WhoAmIRequest) (*userv1.WhoAmIResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.authenticated(ctx)
	s.whoCalls++
	return &userv1.WhoAmIResponse{User: &userv1.User{Id: testListUserID}}, nil
}

func (s *organizationPagesServer) ListUserOrgs(
	ctx context.Context,
	req *orgv1.ListUserOrgsRequest,
) (*orgv1.ListUserOrgsResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.authenticated(ctx)
	if req.GetUserId() != testListUserID {
		s.t.Errorf("user ID changed: %q", req.GetUserId())
	}
	if s.calls >= len(s.pages) {
		s.t.Error("unexpected extra page request")
		return nil, connect.NewError(connect.CodeInternal, errOrganizationListUnavailable.Error())
	}
	expectedCursor := ""
	if s.calls > 0 {
		expectedCursor = s.pages[s.calls-1].next
	}
	if req.GetPageToken() != expectedCursor {
		s.t.Errorf("cursor = %q, want %q", req.GetPageToken(), expectedCursor)
	}
	page := s.pages[s.calls]
	s.calls++
	if page.err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, page.err.Error())
	}
	response := &orgv1.ListUserOrgsResponse{NextPageToken: page.next}
	if page.name != "" {
		response.Orgs = []*orgv1.Organization{{Name: page.name, Id: page.id}}
	}
	return response, nil
}

func TestOrganizationListPagination(t *testing.T) {
	cases := []struct {
		name    string
		pages   []organizationPage
		output  string
		failure string
	}{
		{
			name: "all pages including an empty intermediate page",
			pages: []organizationPage{
				{id: testFirstOrgID, name: testFirstOrgName, next: "opaque/cursor"},
				{next: "after-empty"},
				{id: testLastOrgID, name: "Last"},
			},
			output: "Organizations:\n  - First (ID: " + testFirstOrgID + ")\n  - Last (ID: " + testLastOrgID + ")\n",
		},
		{
			name: "later page failure",
			pages: []organizationPage{
				{id: testFirstOrgID, name: testFirstOrgName, next: "next"},
				{err: errOrganizationListUnavailable},
			},
			failure: "organization listing unavailable",
		},
		{
			name:    "repeated cursor",
			pages:   []organizationPage{{id: testFirstOrgID, name: testFirstOrgName, next: "same"}, {next: "same"}},
			failure: "repeated page",
		},
		{name: "empty", pages: []organizationPage{{}}, output: "No organizations found.\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			t.Setenv(keychain.StoreEnvVar, "file")
			credential := rand.Text()
			fake := &organizationPagesServer{t: t, credential: credential, pages: tc.pages}
			server := connect.NewServer()
			orgv1connect.RegisterOrgServiceHandler(server, fake)
			userv1connect.RegisterUserServiceHandler(server, fake)
			mux := http.NewServeMux()
			connecthttp.Mount(mux, server)
			endpoint := httptest.NewServer(mux)
			t.Cleanup(endpoint.Close)
			t.Setenv("LOCO_HOST", endpoint.URL)
			store, err := keychain.ForCurrentUser()
			if err != nil {
				t.Fatal(err)
			}
			err = store.Set(
				keychain.UserToken{Host: endpoint.URL, Token: credential, ExpiresAt: time.Now().Add(time.Hour)},
			)
			if err != nil {
				t.Fatal(err)
			}
			var output bytes.Buffer
			cmd := newListCmd(listDeps{
				NewOrgClient:  func(host, bearer string) orgv1connect.OrgServiceClient { return rpc.ForHost(host, bearer).Org },
				NewUserClient: func(host, bearer string) userv1connect.UserServiceClient { return rpc.ForHost(host, bearer).User },
				Output:        &output,
			})
			cmd.SilenceErrors = true
			cmd.SilenceUsage = true
			cmd.SetArgs([]string{})
			err = cmd.Execute()
			if tc.failure != "" {
				if err == nil || !strings.Contains(err.Error(), tc.failure) {
					t.Fatalf("error = %v, want %q", err, tc.failure)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if output.String() != tc.output {
				t.Fatalf("output = %q, want %q", output.String(), tc.output)
			}
			fake.mu.Lock()
			defer fake.mu.Unlock()
			if fake.calls != len(tc.pages) || fake.whoCalls != 1 {
				t.Fatalf("list calls = %d, want %d; WhoAmI = %d, want 1", fake.calls, len(tc.pages), fake.whoCalls)
			}
		})
	}
}
