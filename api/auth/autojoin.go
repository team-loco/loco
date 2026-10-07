package auth

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/tvm"
)

func AutoJoin(ctx context.Context, q genDb.Querier, userID uuid.UUID, email string) (bool, error) {
	domain := emailDomain(email)
	if domain == "" {
		return false, nil
	}
	rule, err := q.GetAutoJoinForDomain(ctx, domain)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("look up auto-join: %w", err)
	}
	if rule.AutoJoinScope == nil {
		return false, nil
	}
	return grantOrgScope(ctx, q, userID, rule.OrgID, *rule.AutoJoinScope, domain)
}

func grantOrgScope(
	ctx context.Context,
	q genDb.Querier,
	userID uuid.UUID,
	orgID uuid.UUID,
	scope genDb.Scope,
	domain string,
) (bool, error) {
	current, err := q.GetUserScopes(ctx, userID)
	if err != nil {
		return false, fmt.Errorf("load scopes: %w", err)
	}
	for _, s := range current {
		if s.EntityType == genDb.EntityTypeOrganization && s.EntityID == orgID {
			return false, nil
		}
	}
	grants := make([]genDb.EntityScope, 0, 3)
	for _, s := range tvm.ScopesUpTo(scope) {
		grants = append(grants, genDb.EntityScope{EntityType: genDb.EntityTypeOrganization, EntityID: orgID, Scope: s})
	}
	if err := tvm.ApplyRoles(ctx, q, userID, grants, nil); err != nil {
		return false, fmt.Errorf("grant auto-join scopes: %w", err)
	}
	if err := events.RecordWith(ctx, q, events.Event{
		Type:        events.MemberAdded,
		OrgID:       new(orgID),
		ActorType:   events.ActorSystem,
		SubjectType: events.SubjectUser,
		SubjectID:   new(userID),
		Data:        map[string]any{"scope": scope, "autoJoin": domain},
	}); err != nil {
		return false, err
	}
	return true, nil
}

func AutoJoinDomain(
	ctx context.Context,
	q genDb.Querier,
	orgID uuid.UUID,
	domain string,
	scope genDb.Scope,
) (int, error) {
	users, err := q.ListUsersWithVerifiedEmailDomain(ctx, &domain)
	if err != nil {
		return 0, fmt.Errorf("list users on domain: %w", err)
	}
	added := 0
	for _, userID := range users {
		joined, joinErr := grantOrgScope(ctx, q, userID, orgID, scope, domain)
		if joinErr != nil {
			return added, joinErr
		}
		if joined {
			added++
		}
	}
	return added, nil
}
