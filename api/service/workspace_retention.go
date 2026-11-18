package service

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/team-loco/loco/api/authz/actions"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/limits"
	configv1 "github.com/team-loco/loco/gen/go/loco/config/v1"
	workspacev1 "github.com/team-loco/loco/gen/go/loco/workspace/v1"
	"github.com/team-loco/loco/internal/rpcerr"
)

var (
	errNotRetentionField = rpcerr.NewCoded(
		connect.CodeInvalidArgument,
		"update_mask names a field that is not a retention",
	)
	errRetentionOutOfRange = rpcerr.NewCoded(
		connect.CodeInvalidArgument,
		"retention is outside the organization's limit",
	)
)

// retentionSignal is one RetentionDays field and the organization limit that caps it.
type retentionSignal struct {
	field string
	limit limits.Limit
	days  func(*workspacev1.RetentionDays) **int32
}

var retentionSignals = []retentionSignal{
	{
		field: "log_days", limit: limits.LogRetentionDays,
		days: func(d *workspacev1.RetentionDays) **int32 { return &d.LogDays },
	},
	{
		field: "trace_days", limit: limits.TraceRetentionDays,
		days: func(d *workspacev1.RetentionDays) **int32 { return &d.TraceDays },
	},
	{
		field: "metric_days", limit: limits.MetricRetentionDays,
		days: func(d *workspacev1.RetentionDays) **int32 { return &d.MetricDays },
	},
}

func (s *WorkspaceServer) GetWorkspaceRetention(
	ctx context.Context,
	req *workspacev1.GetWorkspaceRetentionRequest,
) (*workspacev1.GetWorkspaceRetentionResponse, error) {
	workspaceID, err := parseID("workspace_id", req.GetWorkspaceId())
	if err != nil {
		return nil, err
	}
	if err = authorize(ctx, s.authz, actions.GetWorkspaceRetention, workspaceID); err != nil {
		return nil, err
	}
	counts, settings, err := readWorkspaceRetention(ctx, s.queries, s.counts, workspaceID)
	if err != nil {
		return nil, err
	}
	retention, err := workspaceRetention(counts, settings)
	if err != nil {
		return nil, err
	}
	return &workspacev1.GetWorkspaceRetentionResponse{Retention: retention}, nil
}

func (s *WorkspaceServer) SetWorkspaceRetention(
	ctx context.Context,
	req *workspacev1.SetWorkspaceRetentionRequest,
) (*workspacev1.SetWorkspaceRetentionResponse, error) {
	workspaceID, err := parseID("workspace_id", req.GetWorkspaceId())
	if err != nil {
		return nil, err
	}
	if err = authorize(ctx, s.authz, actions.SetWorkspaceRetention, workspaceID); err != nil {
		return nil, err
	}
	masked, err := maskedRetention(req.GetUpdateMask().GetPaths())
	if err != nil {
		return nil, err
	}
	var saved *workspacev1.WorkspaceRetention
	err = withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		if _, lockErr := qtx.LockWorkspaceForCreate(ctx, workspaceID); errors.Is(lockErr, pgx.ErrNoRows) {
			return ErrWorkspaceNotFound
		} else if lockErr != nil {
			return fmt.Errorf("lock workspace: %w", lockErr)
		}
		counts, before, readErr := readWorkspaceRetention(ctx, qtx, s.counts, workspaceID)
		if readErr != nil {
			return readErr
		}
		after := &workspacev1.RetentionDays{
			LogDays: before.LogDays, TraceDays: before.TraceDays, MetricDays: before.MetricDays,
		}
		for _, signal := range masked {
			requested := *signal.days(req.GetSettings())
			if requested == nil {
				*signal.days(after) = nil
				continue
			}
			limit, limitErr := signal.limit.Get(counts)
			if limitErr != nil {
				return limitErr
			}
			if *requested < 1 || *requested > limit {
				return errRetentionOutOfRange.Detail(
					"%s must be from 1 to %d days, the organization's %s", signal.field, limit, signal.limit.Field,
				)
			}
			*signal.days(after) = new(*requested)
		}
		if setErr := qtx.SetWorkspaceRetention(ctx, genDb.SetWorkspaceRetentionParams{
			ID:                  workspaceID,
			LogRetentionDays:    after.LogDays,
			TraceRetentionDays:  after.TraceDays,
			MetricRetentionDays: after.MetricDays,
		}); setErr != nil {
			return fmt.Errorf("set workspace retention: %w", setErr)
		}
		if eventErr := events.Record(ctx, qtx, events.Event{
			Type:        events.WorkspaceUpdated,
			WorkspaceID: new(workspaceID),
			SubjectType: events.SubjectWorkspace,
			SubjectID:   new(workspaceID),
			Data: map[string]any{
				events.FieldOld: retentionData(before),
				events.FieldNew: retentionData(after),
			},
		}); eventErr != nil {
			return eventErr
		}
		var retentionErr error
		saved, retentionErr = workspaceRetention(counts, after)
		return retentionErr
	})
	if err != nil {
		return nil, err
	}
	return &workspacev1.SetWorkspaceRetentionResponse{Retention: saved}, nil
}

// workspaceRetention reports settings against the organization's limits in counts.
func workspaceRetention(
	counts limits.Effective,
	settings *workspacev1.RetentionDays,
) (*workspacev1.WorkspaceRetention, error) {
	result := &workspacev1.WorkspaceRetention{
		Settings:  settings,
		Limits:    &workspacev1.RetentionDays{},
		Effective: &workspacev1.RetentionDays{},
	}
	for _, signal := range retentionSignals {
		limit, err := signal.limit.Get(counts)
		if err != nil {
			return nil, err
		}
		*signal.days(result.GetLimits()) = new(limit)
		effective := limit
		if set := *signal.days(settings); set != nil {
			effective = min(*set, limit)
		}
		*signal.days(result.GetEffective()) = new(effective)
	}
	return result, nil
}

// readWorkspaceRetention returns the limits of the workspace's organization and the workspace's
// own retention settings.
func readWorkspaceRetention(
	ctx context.Context,
	q genDb.Querier,
	defaults *configv1.CountLimits,
	workspaceID uuid.UUID,
) (limits.Effective, *workspacev1.RetentionDays, error) {
	row, err := q.GetWorkspaceRetention(ctx, workspaceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return limits.Effective{}, nil, ErrWorkspaceNotFound
	}
	if err != nil {
		return limits.Effective{}, nil, fmt.Errorf("get workspace retention: %w", err)
	}
	counts, err := limits.ForOrg(ctx, q, defaults, row.OrgID)
	if err != nil {
		return limits.Effective{}, nil, err
	}
	settings := &workspacev1.RetentionDays{
		LogDays:    row.LogRetentionDays,
		TraceDays:  row.TraceRetentionDays,
		MetricDays: row.MetricRetentionDays,
	}
	return counts, settings, nil
}

// maskedRetention returns the signals an update mask names.
func maskedRetention(paths []string) ([]retentionSignal, error) {
	masked := make([]retentionSignal, 0, len(paths))
	for _, path := range paths {
		found := false
		for _, signal := range retentionSignals {
			if signal.field == path {
				masked = append(masked, signal)
				found = true
			}
		}
		if !found {
			return nil, errNotRetentionField.Detail("%s", path)
		}
	}
	return masked, nil
}

// retentionData records settings in an event, with null for a signal left to the limit.
func retentionData(settings *workspacev1.RetentionDays) map[string]*int32 {
	data := make(map[string]*int32, len(retentionSignals))
	for _, signal := range retentionSignals {
		data[signal.field] = *signal.days(settings)
	}
	return data
}
