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
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/tracing"
	"github.com/team-loco/loco/api/reqctx"
	"go.opentelemetry.io/otel/attribute"
)

const (
	UserCreated          = "user.created"
	UserUpdated          = "user.updated"
	UserDeleted          = "user.deleted"
	SystemAdminClaimed   = "system_admin.claimed"
	SystemAdminRevoked   = "system_admin.revoked"
	SystemAdminGranted   = "system_admin.granted"
	CLILoginApproved     = "cli_login.approved"
	CLILoginCompleted    = "cli_login.completed"
	OrgCreated           = "org.created"
	OrgUpdated           = "org.updated"
	OrgDeleted           = "org.deleted"
	OrgSuspended         = "org.suspended"
	OrgResumed           = "org.resumed"
	OrgLimitsChanged     = "org.limits_changed"
	WorkspaceCreated     = "workspace.created"
	WorkspaceUpdated     = "workspace.updated"
	WorkspaceDeleted     = "workspace.deleted"
	MemberAdded          = "member.added"
	MemberRemoved        = "member.removed"
	OrgMemberAdded       = "org_member.added"
	OrgMemberRoleChanged = "org_member.role_changed"
	OrgMemberRemoved     = "org_member.removed"
	InvitationCreated    = "invitation.created"
	InvitationResent     = "invitation.resent"
	InvitationClaimed    = "invitation.claimed"
	InvitationRevoked    = "invitation.revoked"
	UserSuspended        = "user.suspended"
	UserReinstated       = "user.reinstated"
	EnvironmentCreated   = "environment.created"
	EnvironmentUpdated   = "environment.updated"
	EnvironmentDeleted   = "environment.deleted"
	ResourceDegraded     = "resource.degraded"
	ResourceRecovered    = "resource.recovered"
	ResourceCreated      = "resource.created"
	ResourceUpdated      = "resource.updated"
	ResourceDeleted      = "resource.deleted"
	ResourceScaled       = "resource.scaled"
	ResourceRestarted    = "resource.restarted"
	ResourcePartial      = "resource.partial_transferred"
	DeploymentCreated    = "deployment.created"
	DeploymentFailed     = "deployment.failed"
	DeploymentDeleted    = "deployment.deleted"
	BuildCreated         = "build.created"
	BuildStarted         = "build.started"
	BuildSucceeded       = "build.succeeded"
	BuildFailed          = "build.failed"
	BuildCanceled        = "build.canceled"
	DomainCreated        = "domain.created"
	DomainUpdated        = "domain.updated"
	DomainDeleted        = "domain.deleted"
	TokenCreated         = "token.created"
	TokenRevoked         = "token.revoked"
	ClusterRegistered    = "cluster.registered"
	WebhookCreated       = "webhook.created"
	WebhookUpdated       = "webhook.updated"
	WebhookDeleted       = "webhook.deleted"
	WebhookSecretRotated = "webhook.secret_rotated"
	// WebhookTest is matched by name in the events_enqueue_webhooks trigger
	// (migrations/00004_events.sql); update it too.
	WebhookTest        = "webhook.test"
	SecretSet          = "secret.set"
	SecretDeleted      = "secret.deleted"
	SecretKeyRewrapped = "secret_key.rewrapped"
)

const (
	retentionSpan    = "events.retention"
	deletedAttribute = "loco.events.deleted"

	// Each batch is one statement; its cascade into webhook_deliveries must finish within the statement timeout.
	retentionBatchSize = 5000
)

// Sources of a system:admin grant, recorded as FieldSource.
const (
	SourceConfig = "config"
	SourceGrant  = "grant"
)

const ActorSystem = "system"

// Machine-readable causes of build.failed, recorded as FieldReason.
const (
	BuildFailureUploadExpired = "source_upload_expired"
	BuildFailureBuild         = "build_failed"
)

const (
	SubjectUser        = "user"
	SubjectOrg         = "organization"
	SubjectWorkspace   = "workspace"
	SubjectEnvironment = "environment"
	SubjectResource    = "resource"
	SubjectDeployment  = "deployment"
	SubjectBuild       = "build"
	SubjectDomain      = "domain"
	SubjectToken       = "token"
	SubjectCluster     = "cluster"
	SubjectWebhook     = "webhook"
	SubjectInvitation  = "invitation"
)

const (
	FieldName          = "name"
	FieldAction        = "action"
	FieldDomain        = "domain"
	FieldResourceID    = "resourceId"
	FieldDeploymentID  = "deploymentId"
	FieldBuildID       = "buildId"
	FieldImageDigest   = "imageDigest"
	FieldMessage       = "message"
	FieldPartial       = "partial"
	FieldFrom          = "from"
	FieldPaths         = "paths"
	FieldPrimary       = "primary"
	FieldNames         = "names"
	FieldRevision      = "revision"
	FieldKekID         = "kekId"
	FieldEnvironmentID = "environmentId"
	FieldNewDEK        = "newDek"
	FieldReason        = "reason"
	FieldPlacementID   = "placementId"
	FieldRegion        = "region"
	FieldEmail         = "email"
	FieldOrgName       = "org_name"
	FieldInviterName   = "inviter_name"
	FieldInviterEmail  = "inviter_email"
	FieldResourceName  = "resource_name"
	FieldCreatorID     = "creator_id"
	FieldCreatorName   = "creator_name"
	FieldCreatorEmail  = "creator_email"
	FieldExpiresAt     = "expires_at"
	FieldLink          = "link"
	FieldScopes        = "scopes"
	FieldSourceSize    = "sourceSize"
	FieldClusterID     = "clusterId"
	FieldSuperseded    = "superseded"
	FieldIP            = "ip"
	FieldUserAgent     = "userAgent"
	FieldFlow          = "flow"
	FieldIssuer        = "issuer"
	FieldSubject       = "subject"
	FieldIdentities    = "identities"
	FieldSource        = "source"
	FieldEntityType    = "entityType"
	FieldEntityID      = "entityId"
	FieldReplicas      = "replicas"
	FieldCPU           = "cpu"
	FieldMemory        = "memory"
	FieldOld           = "old"
	FieldNew           = "new"
)

type Event struct {
	Type          string
	OrgID         *uuid.UUID
	WorkspaceID   *uuid.UUID
	ResourceID    *uuid.UUID
	EnvironmentID *uuid.UUID
	SubjectType   string
	SubjectID     *uuid.UUID
	ActorType     string
	ActorID       *uuid.UUID
	Data          map[string]any
}

var errActorRequired = errors.New("event actor or principal is required")

func Record(ctx context.Context, q genDb.Querier, ev Event) error {
	actorType, actorID, actorErr := actor(ctx, ev)
	if actorErr != nil {
		return actorErr
	}
	if ev.ResourceID == nil && ev.SubjectType == SubjectResource {
		ev.ResourceID = ev.SubjectID
	}
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
	data := ev.Data
	if data == nil {
		data = map[string]any{}
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("encode event data: %w", err)
	}
	params := genDb.InsertEventParams{
		Type:          ev.Type,
		OrgID:         ev.OrgID,
		WorkspaceID:   ev.WorkspaceID,
		ResourceID:    ev.ResourceID,
		EnvironmentID: ev.EnvironmentID,
		ActorType:     actorType,
		ActorID:       actorID,
		SubjectID:     ev.SubjectID,
		Data:          raw,
	}
	if ev.SubjectType != "" {
		subjectType := ev.SubjectType
		params.SubjectType = &subjectType
	}
	if request, ok := reqctx.RequestFrom(ctx); ok && request.ID != "" {
		params.RequestID = &request.ID
	}
	if traceparent := tracing.Traceparent(ctx); traceparent != "" {
		params.Traceparent = &traceparent
	}
	if _, err := q.InsertEvent(ctx, params); err != nil {
		return fmt.Errorf("insert event: %w", err)
	}
	return nil
}

func actor(ctx context.Context, ev Event) (string, *uuid.UUID, error) {
	if ev.ActorType != "" {
		return ev.ActorType, ev.ActorID, nil
	}
	principal, ok := reqctx.PrincipalFrom(ctx)
	if !ok {
		return "", nil, errActorRequired
	}
	return string(principal.ActorType), &principal.ActorID, nil
}

func RunRetention(ctx context.Context, q genDb.Querier, keep time.Duration, every time.Duration) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		deleteExpired(ctx, q, keep)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func deleteExpired(ctx context.Context, q genDb.Querier, keep time.Duration) {
	ctx, span := tracing.StartRoot(ctx, retentionSpan)
	cutoff := time.Now().Add(-keep)
	var deleted int64
	var err error
	for ctx.Err() == nil {
		var batch int64
		batch, err = q.DeleteEventsBefore(ctx, genDb.DeleteEventsBeforeParams{
			Cutoff:    cutoff,
			BatchSize: retentionBatchSize,
		})
		deleted += batch
		if err != nil || batch < retentionBatchSize {
			break
		}
	}
	span.SetAttributes(attribute.Int64(deletedAttribute, deleted))
	tracing.End(span, err)
	switch {
	case err != nil && ctx.Err() == nil:
		slog.ErrorContext(ctx, "failed to delete expired events", "error", err)
	case deleted > 0:
		slog.InfoContext(ctx, "deleted expired events", "count", deleted)
	}
}

func RecordBuildFailure(
	ctx context.Context,
	q genDb.Querier,
	buildID, resourceID uuid.UUID,
	reason, message string,
) error {
	return RecordBuildOutcome(ctx, q, buildID, resourceID, BuildFailed, map[string]any{
		FieldReason:  reason,
		FieldMessage: message,
	})
}

// RecordBuildOutcome attributes the event to the build's creator. build.failed also names the
// resource and carries the creator's contact details.
func RecordBuildOutcome(
	ctx context.Context,
	q genDb.Querier,
	buildID, resourceID uuid.UUID,
	kind string,
	data map[string]any,
) error {
	creator, err := q.GetBuildCreator(ctx, buildID)
	if err != nil {
		return fmt.Errorf("get build creator: %w", err)
	}
	actorType, actorID := string(genDb.DeploymentActorTypeUser), creator.CreatedBy
	if creator.ApiTokenID != nil {
		actorType, actorID = string(genDb.DeploymentActorTypeApiToken), *creator.ApiTokenID
	}
	data[FieldBuildID] = buildID.String()
	data[FieldResourceID] = resourceID.String()
	if kind == BuildFailed {
		data[FieldResourceName] = creator.ResourceName
		AddCreator(data, creator.CreatorID, creator.CreatorEmail, creator.CreatorName)
	}
	return Record(ctx, q, Event{
		Type:        kind,
		ActorType:   actorType,
		ActorID:     &actorID,
		ResourceID:  new(resourceID),
		SubjectType: SubjectBuild,
		SubjectID:   new(buildID),
		Data:        data,
	})
}

// AddCreator adds the creating user's ID and contact details to data. A nil id means the user no
// longer exists, and the fields are left out.
func AddCreator(data map[string]any, id *uuid.UUID, email, name *string) {
	if id == nil {
		return
	}
	data[FieldCreatorID] = id.String()
	data[FieldCreatorEmail] = *email
	data[FieldCreatorName] = ""
	if name != nil {
		data[FieldCreatorName] = *name
	}
}
