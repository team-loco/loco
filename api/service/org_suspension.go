package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/team-loco/loco/api/authz/actions"
	"github.com/team-loco/loco/api/contextkeys"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/timeutil"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
	"github.com/team-loco/loco/internal/rpcerr"
)

const suspendedBuildMessage = "canceled: the organization was suspended"

var ErrOrgSuspended = errors.New("the organization is suspended and cannot start workloads")

func (s *OrgServer) SuspendOrg(
	ctx context.Context,
	req *orgv1.SuspendOrgRequest,
) (*orgv1.SuspendOrgResponse, error) {
	if err := s.authorizeSystem(ctx, actions.SuspendOrg); err != nil {
		return nil, err
	}
	orgID := uuid.MustParse(req.GetOrgId())
	var org genDb.Organization
	err := withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		current, lockErr := lockOrgForSuspension(ctx, qtx, orgID)
		if lockErr != nil {
			return lockErr
		}
		if current.SuspendedAt != nil {
			org = current
			return nil
		}
		suspended, suspendErr := qtx.SuspendOrg(ctx, genDb.SuspendOrgParams{ID: orgID, Reason: new(req.GetReason())})
		if suspendErr != nil {
			return fmt.Errorf("suspend organization: %w", suspendErr)
		}
		org = suspended
		placements, stopErr := qtx.SuspendOrgPlacements(ctx, orgID)
		if stopErr != nil {
			return fmt.Errorf("stop placements: %w", stopErr)
		}
		clusters := make([]uuid.UUID, 0, len(placements))
		for _, placement := range placements {
			clusters = append(clusters, placement.ClusterID)
		}
		builds, cancelErr := qtx.CancelOrgActiveBuilds(ctx, genDb.CancelOrgActiveBuildsParams{
			Message: suspendedBuildMessage,
			OrgID:   orgID,
		})
		if cancelErr != nil {
			return fmt.Errorf("cancel builds: %w", cancelErr)
		}
		for _, build := range builds {
			if build.ClusterID != nil {
				clusters = append(clusters, *build.ClusterID)
			}
		}
		if notifyErr := notifyClusters(ctx, qtx, clusters); notifyErr != nil {
			return notifyErr
		}
		slog.InfoContext(ctx, "organization suspended",
			"org_id", orgID,
			"placements", len(placements),
			"builds", len(builds),
		)
		return events.Record(ctx, qtx, events.Event{
			Type:        events.OrgSuspended,
			OrgID:       new(orgID),
			SubjectType: events.SubjectOrg,
			SubjectID:   new(orgID),
			Data:        map[string]any{events.FieldReason: req.GetReason()},
		})
	})
	if err != nil {
		return nil, txError(ctx, "failed to suspend org", err)
	}
	return &orgv1.SuspendOrgResponse{Organization: orgToProto(org)}, nil
}

func (s *OrgServer) ResumeOrg(
	ctx context.Context,
	req *orgv1.ResumeOrgRequest,
) (*orgv1.ResumeOrgResponse, error) {
	if err := s.authorizeSystem(ctx, actions.ResumeOrg); err != nil {
		return nil, err
	}
	orgID := uuid.MustParse(req.GetOrgId())
	var org genDb.Organization
	err := withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		current, lockErr := lockOrgForSuspension(ctx, qtx, orgID)
		if lockErr != nil {
			return lockErr
		}
		if current.SuspendedAt == nil {
			org = current
			return nil
		}
		resumed, resumeErr := qtx.ResumeOrg(ctx, orgID)
		if resumeErr != nil {
			return fmt.Errorf("resume organization: %w", resumeErr)
		}
		org = resumed
		placements, restoreErr := qtx.ResumeOrgPlacements(ctx, orgID)
		if restoreErr != nil {
			return fmt.Errorf("restore placements: %w", restoreErr)
		}
		clusters := make([]uuid.UUID, 0, len(placements))
		for _, placement := range placements {
			clusters = append(clusters, placement.ClusterID)
		}
		if notifyErr := notifyClusters(ctx, qtx, clusters); notifyErr != nil {
			return notifyErr
		}
		slog.InfoContext(ctx, "organization resumed", "org_id", orgID, "placements", len(placements))
		return events.Record(ctx, qtx, events.Event{
			Type:        events.OrgResumed,
			OrgID:       new(orgID),
			SubjectType: events.SubjectOrg,
			SubjectID:   new(orgID),
		})
	})
	if err != nil {
		return nil, txError(ctx, "failed to resume org", err)
	}
	return &orgv1.ResumeOrgResponse{Organization: orgToProto(org)}, nil
}

func (s *OrgServer) authorizeSystem(ctx context.Context, action actions.Action) error {
	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return rpcerr.New(connect.CodeInternal, errEntityScopesNotFound)
	}
	required := actions.NewSystem(action)
	if err := s.authz.Check(ctx, scopes, required); err != nil {
		slog.WarnContext(ctx, "unauthorized to change organization suspension")
		return connect.NewError(connect.CodePermissionDenied, publicPermissionDenied).WithCause(err)
	}
	return nil
}

func lockOrgForSuspension(ctx context.Context, qtx *genDb.Queries, orgID uuid.UUID) (genDb.Organization, error) {
	org, err := qtx.LockOrgForSuspension(ctx, orgID)
	if errors.Is(err, pgx.ErrNoRows) {
		return genDb.Organization{}, rpcerr.New(connect.CodeNotFound, ErrOrgNotFound)
	}
	if err != nil {
		return genDb.Organization{}, fmt.Errorf("lock organization: %w", err)
	}
	return org, nil
}

func notifyClusters(ctx context.Context, qtx *genDb.Queries, clusters []uuid.UUID) error {
	notified := make(map[uuid.UUID]struct{}, len(clusters))
	for _, clusterID := range clusters {
		if _, done := notified[clusterID]; done {
			continue
		}
		notified[clusterID] = struct{}{}
		if err := notifyCluster(ctx, qtx, clusterID); err != nil {
			return err
		}
	}
	return nil
}

func lockOrganization(ctx context.Context, qtx *genDb.Queries, workspaceID uuid.UUID) error {
	if err := qtx.LockWorkspaceOrganization(ctx, workspaceID); err != nil {
		return fmt.Errorf("lock organization: %w", err)
	}
	return nil
}

func ensureOrganizationActive(ctx context.Context, qtx *genDb.Queries, resourceID uuid.UUID) error {
	reason, err := qtx.LockResourceOrganization(ctx, resourceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrResourceNotFound
	}
	if err != nil {
		return fmt.Errorf("lock organization: %w", err)
	}
	if reason != nil {
		return rpcerr.New(connect.CodeFailedPrecondition, fmt.Errorf("%w: %s", ErrOrgSuspended, *reason))
	}
	return nil
}

func orgToProto(org genDb.Organization) *orgv1.Organization {
	return &orgv1.Organization{
		Id:               org.ID.String(),
		Name:             org.Name,
		CreatedBy:        org.CreatedBy.String(),
		CreatedAt:        timeutil.ParsePostgresTimestamp(org.CreatedAt),
		UpdatedAt:        timeutil.ParsePostgresTimestamp(org.UpdatedAt),
		SuspendedAt:      timeutil.ParsePostgresTimestampPtr(org.SuspendedAt),
		SuspensionReason: derefString(org.SuspensionReason),
	}
}
