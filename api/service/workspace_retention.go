package service

import (
	"context"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"

	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/limits"
	configv1 "github.com/team-loco/loco/gen/go/loco/config/v1"
	workspacev1 "github.com/team-loco/loco/gen/go/loco/workspace/v1"
	"github.com/team-loco/loco/internal/rpcerr"
)

var errRetentionOutOfRange = rpcerr.NewCoded(
	connect.CodeInvalidArgument,
	"retention is outside the organization's limit",
)

// retentionSignal is one signal's RetentionDays field, its UpdateWorkspaceRequest field and the
// organization limit that caps it, whose name the request field shares.
type retentionSignal struct {
	limit   limits.Limit
	days    func(*workspacev1.RetentionDays) **int32
	request func(*workspacev1.UpdateWorkspaceRequest) *int32
}

var retentionSignals = []retentionSignal{
	{
		limit:   limits.LogRetentionDays,
		days:    func(d *workspacev1.RetentionDays) **int32 { return &d.LogDays },
		request: func(r *workspacev1.UpdateWorkspaceRequest) *int32 { return r.LogRetentionDays },
	},
	{
		limit:   limits.TraceRetentionDays,
		days:    func(d *workspacev1.RetentionDays) **int32 { return &d.TraceDays },
		request: func(r *workspacev1.UpdateWorkspaceRequest) *int32 { return r.TraceRetentionDays },
	},
	{
		limit:   limits.MetricRetentionDays,
		days:    func(d *workspacev1.RetentionDays) **int32 { return &d.MetricDays },
		request: func(r *workspacev1.UpdateWorkspaceRequest) *int32 { return r.MetricRetentionDays },
	},
}

// updateRetention applies the request's retention fields to before under the organization's
// limits in counts. A field of 0 clears the workspace's value.
func updateRetention(
	req *workspacev1.UpdateWorkspaceRequest,
	counts limits.Effective,
	before *workspacev1.RetentionDays,
) (*workspacev1.RetentionDays, error) {
	after := ownRetention(before.LogDays, before.TraceDays, before.MetricDays)
	for _, signal := range retentionSignals {
		requested := signal.request(req)
		if requested == nil {
			continue
		}
		if *requested == 0 {
			*signal.days(after) = nil
			continue
		}
		limit, err := signal.limit.Get(counts)
		if err != nil {
			return nil, err
		}
		if *requested < 1 || *requested > limit {
			return nil, errRetentionOutOfRange.Detail(
				"%s must be from 1 to %d days, the organization's limit, or 0 to clear it",
				signal.limit.Field, limit,
			)
		}
		*signal.days(after) = new(*requested)
	}
	return after, nil
}

// requestsRetention reports whether the request sets any signal's retention.
func requestsRetention(req *workspacev1.UpdateWorkspaceRequest) bool {
	for _, signal := range retentionSignals {
		if signal.request(req) != nil {
			return true
		}
	}
	return false
}

// effectiveRetention caps each of the workspace's own values by the organization's limit in
// counts, and gives a signal without one the limit.
func effectiveRetention(counts limits.Effective, own *workspacev1.RetentionDays) (*workspacev1.RetentionDays, error) {
	effective := &workspacev1.RetentionDays{}
	for _, signal := range retentionSignals {
		limit, err := signal.limit.Get(counts)
		if err != nil {
			return nil, err
		}
		days := limit
		if set := *signal.days(own); set != nil {
			days = min(*set, limit)
		}
		*signal.days(effective) = new(days)
	}
	return effective, nil
}

func ownRetention(logs, traces, metrics *int32) *workspacev1.RetentionDays {
	return &workspacev1.RetentionDays{LogDays: logs, TraceDays: traces, MetricDays: metrics}
}

// retentionData records retention in an event, with null for a signal left to the limit.
func retentionData(retention *workspacev1.RetentionDays) map[string]*int32 {
	data := make(map[string]*int32, len(retentionSignals))
	for _, signal := range retentionSignals {
		data[string(signal.limit.Field)] = *signal.days(retention)
	}
	return data
}

// orgLimits loads each organization's limits once.
type orgLimits struct {
	queries  genDb.Querier
	defaults *configv1.CountLimits
	loaded   map[uuid.UUID]limits.Effective
}

func (o *orgLimits) get(ctx context.Context, orgID uuid.UUID) (limits.Effective, error) {
	if counts, ok := o.loaded[orgID]; ok {
		return counts, nil
	}
	counts, err := limits.ForOrg(ctx, o.queries, o.defaults, orgID)
	if err != nil {
		return limits.Effective{}, err
	}
	if o.loaded == nil {
		o.loaded = map[uuid.UUID]limits.Effective{}
	}
	o.loaded[orgID] = counts
	return counts, nil
}
