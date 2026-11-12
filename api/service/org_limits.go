package service

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/team-loco/loco/api/authz/actions"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/limits"
	configv1 "github.com/team-loco/loco/gen/go/loco/config/v1"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
	"github.com/team-loco/loco/internal/rpcerr"
)

var (
	errLimitNotOverridable = rpcerr.NewCoded(
		connect.CodeInvalidArgument,
		"update_mask names a field an organization cannot override",
	)
	errNegativeLimit = rpcerr.NewCoded(connect.CodeInvalidArgument, "limits must not be negative")
)

func (s *OrgServer) GetOrgLimits(
	ctx context.Context,
	req *orgv1.GetOrgLimitsRequest,
) (*orgv1.GetOrgLimitsResponse, error) {
	orgID, err := parseID("org_id", req.GetOrgId())
	if err != nil {
		return nil, err
	}
	if err = authorize(ctx, s.authz, actions.GetOrgLimits, orgID); err != nil {
		return nil, err
	}
	row, err := s.queries.GetOrgLimitOverrides(ctx, orgID)
	if errors.Is(err, pgx.ErrNoRows) {
		if _, orgErr := s.queries.GetOrgByID(ctx, orgID); errors.Is(orgErr, pgx.ErrNoRows) {
			return nil, ErrOrgNotFound
		} else if orgErr != nil {
			return nil, fmt.Errorf("get organization: %w", orgErr)
		}
		return &orgv1.GetOrgLimitsResponse{Limits: &orgv1.OrgLimits{Limits: s.counts}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get organization limits: %w", err)
	}
	return &orgv1.GetOrgLimitsResponse{Limits: s.orgLimitsToProto(row)}, nil
}

func (s *OrgServer) SetOrgLimits(
	ctx context.Context,
	req *orgv1.SetOrgLimitsRequest,
) (*orgv1.SetOrgLimitsResponse, error) {
	orgID, err := parseID("org_id", req.GetOrgId())
	if err != nil {
		return nil, err
	}
	if err = authorizeSystem(ctx, s.authz, actions.SetOrgLimits); err != nil {
		return nil, err
	}
	masked, err := maskedLimits(req.GetUpdateMask().GetPaths())
	if err != nil {
		return nil, err
	}
	for _, limit := range masked {
		if limit.Get(req.GetLimits()) < 0 {
			return nil, errNegativeLimit
		}
	}
	p, err := principalFrom(ctx)
	if err != nil {
		return nil, err
	}
	var updatedBy *uuid.UUID
	if p.Entity.Type == genDb.EntityTypeUser {
		updatedBy = new(p.Entity.ID)
	}
	var saved genDb.OrgLimitOverride
	err = withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		if _, lockErr := qtx.LockOrgForCreate(ctx, orgID); errors.Is(lockErr, pgx.ErrNoRows) {
			return ErrOrgNotFound
		} else if lockErr != nil {
			return fmt.Errorf("lock organization: %w", lockErr)
		}
		row, getErr := qtx.GetOrgLimitOverrides(ctx, orgID)
		if getErr != nil && !errors.Is(getErr, pgx.ErrNoRows) {
			return fmt.Errorf("get organization limits: %w", getErr)
		}
		before := limits.Apply(s.counts, row)
		for _, limit := range masked {
			value := limit.Get(req.GetLimits())
			column := limit.Column(&row)
			*column = nil
			if value > 0 {
				*column = new(value)
			}
		}
		var upsertErr error
		saved, upsertErr = qtx.UpsertOrgLimitOverrides(ctx, genDb.UpsertOrgLimitOverridesParams{
			OrgID:                    orgID,
			WorkspacesPerOrg:         row.WorkspacesPerOrg,
			MembersPerOrg:            row.MembersPerOrg,
			InvitationsPerOrg:        row.InvitationsPerOrg,
			EnvironmentsPerWorkspace: row.EnvironmentsPerWorkspace,
			ResourcesPerWorkspace:    row.ResourcesPerWorkspace,
			WebhooksPerWorkspace:     row.WebhooksPerWorkspace,
			TokensPerEntity:          row.TokensPerEntity,
			DomainsPerResource:       row.DomainsPerResource,
			SecretsPerResource:       row.SecretsPerResource,
			DeploymentHistory:        row.DeploymentHistory,
			BuildHistory:             row.BuildHistory,
			Reason:                   req.GetReason(),
			UpdatedBy:                updatedBy,
		})
		if upsertErr != nil {
			return fmt.Errorf("save organization limits: %w", upsertErr)
		}
		after := limits.Apply(s.counts, saved)
		old := make(map[string]int32, len(masked))
		changed := make(map[string]int32, len(masked))
		for _, limit := range masked {
			old[string(limit.Field)] = limit.Get(before)
			changed[string(limit.Field)] = limit.Get(after)
		}
		return events.Record(ctx, qtx, events.Event{
			Type:        events.OrgLimitsChanged,
			OrgID:       new(orgID),
			SubjectType: events.SubjectOrg,
			SubjectID:   new(orgID),
			Data: map[string]any{
				events.FieldReason: req.GetReason(),
				events.FieldOld:    old,
				events.FieldNew:    changed,
			},
		})
	})
	if err != nil {
		return nil, err
	}
	return &orgv1.SetOrgLimitsResponse{Limits: s.orgLimitsToProto(saved)}, nil
}

// maskedLimits returns the limits an update mask names, refusing any an organization cannot
// override.
func maskedLimits(paths []string) ([]limits.Limit, error) {
	masked := make([]limits.Limit, 0, len(paths))
	for _, path := range paths {
		found := false
		for _, limit := range limits.All {
			if limit.Field == protoreflect.Name(path) && limit.Overridable() {
				masked = append(masked, limit)
				found = true
			}
		}
		if !found {
			return nil, errLimitNotOverridable.Detail("%s", path)
		}
	}
	return masked, nil
}

func (s *OrgServer) orgLimitsToProto(row genDb.OrgLimitOverride) *orgv1.OrgLimits {
	result := &orgv1.OrgLimits{
		Limits:    limits.Apply(s.counts, row),
		Reason:    row.Reason,
		UpdatedAt: timestamppb.New(row.UpdatedAt),
	}
	if row.UpdatedBy != nil {
		result.UpdatedBy = row.UpdatedBy.String()
	}
	for _, limit := range limits.All {
		if limit.Overridable() && *limit.Column(&row) != nil {
			result.Overridden = append(result.Overridden, string(limit.Field))
		}
	}
	return result
}

// checkOrgLimit refuses adding records to existing ones past the organization's limit.
func checkOrgLimit(
	ctx context.Context,
	qtx *genDb.Queries,
	defaults *configv1.CountLimits,
	limit limits.Limit,
	orgID uuid.UUID,
	existing, adding int64,
) error {
	effective, err := limits.ForOrg(ctx, qtx, defaults, orgID)
	if err != nil {
		return err
	}
	return limit.Check(effective, existing, adding)
}

// checkResourceLimit is checkOrgLimit for the organization of a resource.
func checkResourceLimit(
	ctx context.Context,
	qtx *genDb.Queries,
	defaults *configv1.CountLimits,
	limit limits.Limit,
	resourceID uuid.UUID,
	existing, adding int64,
) error {
	owner, err := qtx.GetWorkspaceOrganizationIDByResourceID(ctx, resourceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrResourceNotFound
	}
	if err != nil {
		return fmt.Errorf("get resource organization: %w", err)
	}
	return checkOrgLimit(ctx, qtx, defaults, limit, owner.OrgID, existing, adding)
}

// checkWorkspaceLimit locks the workspace against concurrent creates, counts what the limit
// caps in it and refuses one more past the organization's limit.
func checkWorkspaceLimit(
	ctx context.Context,
	qtx *genDb.Queries,
	defaults *configv1.CountLimits,
	limit limits.Limit,
	workspaceID uuid.UUID,
	count func(context.Context, uuid.UUID) (int64, error),
) error {
	orgID, err := qtx.LockWorkspaceForCreate(ctx, workspaceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrWorkspaceNotFound
	}
	if err != nil {
		return fmt.Errorf("lock workspace: %w", err)
	}
	existing, err := count(ctx, workspaceID)
	if err != nil {
		return fmt.Errorf("count %s: %w", limit.Field, err)
	}
	return checkOrgLimit(ctx, qtx, defaults, limit, orgID, existing, 1)
}
