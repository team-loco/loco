package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
)

const (
	UserCreated          = "user.created"
	UserUpdated          = "user.updated"
	UserDeleted          = "user.deleted"
	IdentityLinked       = "identity.linked"
	IdentityRevoked      = "identity.revoked"
	CLILoginApproved     = "cli_login.approved"
	CLILoginCompleted    = "cli_login.completed"
	OrgCreated           = "org.created"
	OrgUpdated           = "org.updated"
	OrgDeleted           = "org.deleted"
	WorkspaceCreated     = "workspace.created"
	WorkspaceUpdated     = "workspace.updated"
	WorkspaceDeleted     = "workspace.deleted"
	MemberAdded          = "member.added"
	MemberRemoved        = "member.removed"
	EnvironmentCreated   = "environment.created"
	EnvironmentUpdated   = "environment.updated"
	EnvironmentDeleted   = "environment.deleted"
	ResourceCreated      = "resource.created"
	ResourceUpdated      = "resource.updated"
	ResourceDeleted      = "resource.deleted"
	ResourceScaled       = "resource.scaled"
	ResourceEnvUpdated   = "resource.env_updated"
	DeploymentCreated    = "deployment.created"
	DeploymentDeleted    = "deployment.deleted"
	BuildCreated         = "build.created"
	BuildStarted         = "build.started"
	BuildCanceled        = "build.canceled"
	DomainCreated        = "domain.created"
	DomainUpdated        = "domain.updated"
	DomainDeleted        = "domain.deleted"
	PlatformDomainChange = "platform_domain.changed"
	TokenCreated         = "token.created"
	TokenRevoked         = "token.revoked"
	ClusterRegistered    = "cluster.registered"
	WebhookCreated       = "webhook.created"
	WebhookDeleted       = "webhook.deleted"
)

const (
	ActorSystem    = "system"
	ActorAnonymous = "anonymous"
)

const (
	SubjectUser           = "user"
	SubjectOrg            = "organization"
	SubjectWorkspace      = "workspace"
	SubjectEnvironment    = "environment"
	SubjectResource       = "resource"
	SubjectDeployment     = "deployment"
	SubjectBuild          = "build"
	SubjectDomain         = "domain"
	SubjectPlatformDomain = "platform_domain"
	SubjectToken          = "token"
	SubjectCluster        = "cluster"
	SubjectWebhook        = "webhook"
)

const (
	FieldName       = "name"
	FieldAction     = "action"
	FieldDomain     = "domain"
	FieldResourceID = "resourceId"
)

type Event struct {
	Type        string
	OrgID       *uuid.UUID
	WorkspaceID *uuid.UUID
	ResourceID  *uuid.UUID
	SubjectType string
	SubjectID   *uuid.UUID
	ActorType   string
	ActorID     *uuid.UUID
	Data        map[string]any
}

func Record(ctx context.Context, q genDb.Querier, ev Event) error {
	if ev.WorkspaceID == nil && ev.ResourceID != nil {
		res, err := q.GetResourceByID(ctx, *ev.ResourceID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("look up event resource: %w", err)
		}
		if err == nil {
			ev.WorkspaceID = &res.WorkspaceID
		}
	}
	if ev.OrgID == nil && ev.WorkspaceID != nil {
		orgID, err := q.GetOrganizationIDByWorkspaceID(ctx, *ev.WorkspaceID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("look up event workspace: %w", err)
		}
		if err == nil {
			ev.OrgID = &orgID
		}
	}
	actorType, actorID := actor(ctx, ev)
	data := ev.Data
	if data == nil {
		data = map[string]any{}
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("encode event data: %w", err)
	}
	params := genDb.InsertEventParams{
		Type:        ev.Type,
		OrgID:       ev.OrgID,
		WorkspaceID: ev.WorkspaceID,
		ActorType:   actorType,
		ActorID:     actorID,
		SubjectID:   ev.SubjectID,
		Data:        raw,
	}
	if ev.SubjectType != "" {
		subjectType := ev.SubjectType
		params.SubjectType = &subjectType
	}
	if rid, ok := ctx.Value(contextkeys.RequestIDKey).(string); ok && rid != "" {
		params.RequestID = &rid
	}
	if _, err := q.InsertEvent(ctx, params); err != nil {
		return fmt.Errorf("insert event: %w", err)
	}
	return nil
}

func actor(ctx context.Context, ev Event) (string, *uuid.UUID) {
	if ev.ActorType != "" {
		return ev.ActorType, ev.ActorID
	}
	entity, ok := ctx.Value(contextkeys.EntityKey).(genDb.Entity)
	if !ok {
		return ActorAnonymous, nil
	}
	id := entity.ID
	return string(entity.Type), &id
}

func RunRetention(ctx context.Context, q genDb.Querier, keep time.Duration, every time.Duration) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		deleted, err := q.DeleteEventsBefore(ctx, time.Now().Add(-keep))
		switch {
		case err != nil && ctx.Err() == nil:
			slog.ErrorContext(ctx, "failed to delete expired events", "error", err)
		case deleted > 0:
			slog.InfoContext(ctx, "deleted expired events", "count", deleted)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
