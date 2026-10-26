package token

import (
	"context"
	"crypto/rand"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/spf13/cobra"
	"github.com/team-loco/loco/cmd/internal/keychain"
	tokenv1 "github.com/team-loco/loco/gen/go/loco/token/v1"
	"github.com/team-loco/loco/gen/go/loco/token/v1/tokenv1connect"
	userv1 "github.com/team-loco/loco/gen/go/loco/user/v1"
	"github.com/team-loco/loco/gen/go/loco/user/v1/userv1connect"
	"github.com/team-loco/loco/internal/rpc"
)

const (
	testWorkspaceEntity = "workspace"
	testEntityID        = "00000000-0000-7000-8000-000000000001"
	testCurrentUserID   = "00000000-0000-7000-8000-000000000002"
	testTokenName       = "example-token"
	testCreateAction    = "create"
	testListAction      = "list"
	testDeleteAction    = "delete"
	testTokenLifetime   = 60
)

type tokenIDsServer struct {
	tokenv1connect.UnimplementedTokenServiceHandler
	userv1connect.UnimplementedUserServiceHandler
	t           *testing.T
	credential  string
	entityType  tokenv1.EntityType
	entityID    string
	calls       int
	whoAmICalls int
}

func (s *tokenIDsServer) record(ctx context.Context, entityType tokenv1.EntityType, entityID string) {
	s.t.Helper()
	info, ok := connect.CallInfoForServerContext(ctx)
	if !ok || info.RequestHeader().Get("Authorization") != "Bearer "+s.credential {
		s.t.Error("token request is not authenticated")
	}
	if entityType != s.entityType || entityID != s.entityID {
		s.t.Errorf("entity = %s/%s, want %s/%s", entityType, entityID, s.entityType, s.entityID)
	}
	s.calls++
}

func (s *tokenIDsServer) CreateToken(
	ctx context.Context,
	req *tokenv1.CreateTokenRequest,
) (*tokenv1.CreateTokenResponse, error) {
	s.record(ctx, req.GetEntityType(), req.GetEntityId())
	if req.GetName() != testTokenName || req.GetExpiresInSec() != testTokenLifetime || len(req.GetScopes()) != 1 {
		s.t.Errorf("unexpected create request: %v", req)
	}
	for _, scope := range req.GetScopes() {
		if scope.GetEntityId() != s.entityID || scope.GetEntityType() != s.entityType ||
			scope.GetScope() != tokenv1.Scope_SCOPE_READ {
			s.t.Errorf("unexpected scope: %v", scope)
		}
	}
	return &tokenv1.CreateTokenResponse{TokenMetadata: &tokenv1.Token{Name: testTokenName}}, nil
}

func (s *tokenIDsServer) ListTokens(
	ctx context.Context,
	req *tokenv1.ListTokensRequest,
) (*tokenv1.ListTokensResponse, error) {
	s.record(ctx, req.GetEntityType(), req.GetEntityId())
	return &tokenv1.ListTokensResponse{}, nil
}

func (s *tokenIDsServer) RevokeToken(
	ctx context.Context,
	req *tokenv1.RevokeTokenRequest,
) (*tokenv1.RevokeTokenResponse, error) {
	s.record(ctx, req.GetEntityType(), req.GetEntityId())
	if req.GetName() != testTokenName {
		s.t.Errorf("revoked token name = %q", req.GetName())
	}
	return &tokenv1.RevokeTokenResponse{}, nil
}

func (s *tokenIDsServer) WhoAmI(_ context.Context, _ *userv1.WhoAmIRequest) (*userv1.WhoAmIResponse, error) {
	s.whoAmICalls++
	return &userv1.WhoAmIResponse{User: &userv1.User{Id: testCurrentUserID}}, nil
}

func tokenIDCommand(t *testing.T, fake *tokenIDsServer, action string) *cobra.Command {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv(keychain.StoreEnvVar, "file")
	fake.credential = rand.Text()
	server := connect.NewServer()
	tokenv1connect.RegisterTokenServiceHandler(server, fake)
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
	if err := store.Set(
		keychain.UserToken{Host: endpoint.URL, Token: fake.credential, ExpiresAt: time.Now().Add(time.Hour)},
	); err != nil {
		t.Fatal(err)
	}
	newTokenClient := func(host, bearer string) tokenv1connect.TokenServiceClient {
		return rpc.ForHost(host, bearer).Token
	}
	newUserClient := func(host, bearer string) userv1connect.UserServiceClient { return rpc.ForHost(host, bearer).User }
	switch action {
	case testCreateAction:
		return newCreateCmd(
			createDeps{NewTokenClient: newTokenClient, NewUserClient: newUserClient, Output: io.Discard},
		)
	case testListAction:
		return newListCmd(listDeps{NewTokenClient: newTokenClient, NewUserClient: newUserClient, Output: io.Discard})
	case testDeleteAction:
		return newDeleteCmd(
			deleteDeps{NewTokenClient: newTokenClient, NewUserClient: newUserClient, Output: io.Discard},
		)
	default:
		t.Fatalf("unknown action %q", action)
		return nil
	}
}

func TestTokenCommandsUseUUIDs(t *testing.T) {
	entities := []struct {
		name     string
		kind     tokenv1.EntityType
		id       string
		explicit bool
	}{
		{name: "user", kind: tokenv1.EntityType_ENTITY_TYPE_USER, id: testEntityID, explicit: true},
		{name: "org", kind: tokenv1.EntityType_ENTITY_TYPE_ORGANIZATION, id: testEntityID, explicit: true},
		{name: testWorkspaceEntity, kind: tokenv1.EntityType_ENTITY_TYPE_WORKSPACE, id: testEntityID, explicit: true},
		{name: "resource", kind: tokenv1.EntityType_ENTITY_TYPE_RESOURCE, id: testEntityID, explicit: true},
		{name: "personal", kind: tokenv1.EntityType_ENTITY_TYPE_USER, id: testCurrentUserID},
	}
	for _, entity := range entities {
		for _, action := range []string{testCreateAction, testListAction, testDeleteAction} {
			t.Run(entity.name+"/"+action, func(t *testing.T) {
				fake := &tokenIDsServer{t: t, entityType: entity.kind, entityID: entity.id}
				cmd := tokenIDCommand(t, fake, action)
				cmd.SilenceErrors = true
				cmd.SilenceUsage = true
				var args []string
				if action != testListAction {
					args = append(args, testTokenName)
				}
				if action == testCreateAction {
					args = append(args, "--expires", "60s")
				}
				if action == testDeleteAction {
					args = append(args, "--yes")
				}
				if entity.explicit {
					args = append(args, "--entity-type", entity.name, "--entity-id", entity.id)
				}
				cmd.SetArgs(args)
				if err := cmd.Execute(); err != nil {
					t.Fatal(err)
				}
				if fake.calls != 1 {
					t.Fatalf("token RPC calls = %d, want 1", fake.calls)
				}
				expectedWhoAmI := 0
				if !entity.explicit {
					expectedWhoAmI = 1
				}
				if fake.whoAmICalls != expectedWhoAmI {
					t.Fatalf("WhoAmI calls = %d, want %d", fake.whoAmICalls, expectedWhoAmI)
				}
			})
		}
	}
}

func TestTokenCommandsRequireNonUserEntityID(t *testing.T) {
	for _, action := range []string{testCreateAction, testListAction, testDeleteAction} {
		t.Run(action, func(t *testing.T) {
			fake := &tokenIDsServer{t: t}
			cmd := tokenIDCommand(t, fake, action)
			cmd.SilenceErrors = true
			cmd.SilenceUsage = true
			args := []string{"--entity-type", testWorkspaceEntity}
			if action != testListAction {
				args = append(args, testTokenName)
			}
			if action == testDeleteAction {
				args = append(args, "--yes")
			}
			cmd.SetArgs(args)
			err := cmd.Execute()
			if err == nil || !strings.Contains(err.Error(), "--entity-id is required") {
				t.Fatalf("missing entity ID: %v", err)
			}
			if fake.calls != 0 || fake.whoAmICalls != 0 {
				t.Fatal("missing entity ID made an RPC")
			}
		})
	}
}
