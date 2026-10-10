package service

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	secretv1 "github.com/team-loco/loco/gen/go/loco/secret/v1"
)

type environmentLookupQuerier struct {
	orgLookupQuerier
	env genDb.Environment
}

func (q environmentLookupQuerier) GetEnvironmentByID(_ context.Context, _ uuid.UUID) (genDb.Environment, error) {
	return q.env, nil
}

func secretAuthzContext(t *testing.T, scopes ...genDb.EntityScope) context.Context {
	t.Helper()
	entity := genDb.Entity{Type: genDb.EntityTypeUser, ID: uuid.Must(uuid.NewV7())}
	ctx := context.WithValue(t.Context(), contextkeys.EntityKey, entity)
	return context.WithValue(ctx, contextkeys.EntityScopesKey, scopes)
}

func TestSecretServiceAuthorization(t *testing.T) {
	workspaceID := uuid.Must(uuid.NewV7())
	env := genDb.Environment{ID: uuid.Must(uuid.NewV7()), WorkspaceID: workspaceID}
	envID := env.ID.String()
	querier := environmentLookupQuerier{orgLookupQuerier{orgID: uuid.Must(uuid.NewV7())}, env}
	server := NewSecretServer(nil, querier, nil, SecretConfig{})
	read := genDb.EntityScope{EntityType: genDb.EntityTypeWorkspace, EntityID: workspaceID, Scope: genDb.ScopeRead}
	write := genDb.EntityScope{EntityType: genDb.EntityTypeWorkspace, EntityID: workspaceID, Scope: genDb.ScopeWrite}
	otherWrite := genDb.EntityScope{
		EntityType: genDb.EntityTypeWorkspace, EntityID: uuid.Must(uuid.NewV7()), Scope: genDb.ScopeWrite,
	}

	tests := []struct {
		name   string
		scopes []genDb.EntityScope
		call   func(context.Context) error
		want   connect.Code
	}{
		{"set with read only", []genDb.EntityScope{read}, func(ctx context.Context) error {
			_, err := server.SetSecrets(ctx, connect.NewRequest(&secretv1.SetSecretsRequest{
				EnvironmentId: envID, Values: map[string]string{"A": "1"},
			}))
			return err
		}, connect.CodePermissionDenied},
		{"set on another workspace", []genDb.EntityScope{otherWrite}, func(ctx context.Context) error {
			_, err := server.SetSecrets(ctx, connect.NewRequest(&secretv1.SetSecretsRequest{
				EnvironmentId: envID, Values: map[string]string{"A": "1"},
			}))
			return err
		}, connect.CodePermissionDenied},
		{"set without a provider", []genDb.EntityScope{write}, func(ctx context.Context) error {
			_, err := server.SetSecrets(ctx, connect.NewRequest(&secretv1.SetSecretsRequest{
				EnvironmentId: envID, Values: map[string]string{"A": "1"},
			}))
			return err
		}, connect.CodeFailedPrecondition},
		{"delete with read only", []genDb.EntityScope{read}, func(ctx context.Context) error {
			_, err := server.DeleteSecrets(ctx, connect.NewRequest(&secretv1.DeleteSecretsRequest{
				EnvironmentId: envID, Names: []string{"A"},
			}))
			return err
		}, connect.CodePermissionDenied},
		{"list without read", []genDb.EntityScope{otherWrite}, func(ctx context.Context) error {
			_, err := server.ListSecrets(ctx, connect.NewRequest(&secretv1.ListSecretsRequest{EnvironmentId: envID}))
			return err
		}, connect.CodePermissionDenied},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.call(secretAuthzContext(t, tt.scopes...))
			if code := connect.CodeOf(err); code != tt.want {
				t.Fatalf("code = %v (%v), want %v", code, err, tt.want)
			}
		})
	}
}
