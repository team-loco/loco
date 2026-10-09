package service

import (
	"context"
	"errors"
	"log/slog"

	"connectrpc.com/connect"
	"github.com/team-loco/loco/api/authz/actions"
	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/configplan"
	planv1 "github.com/team-loco/loco/gen/go/loco/plan/v1"
)

var (
	errPlanHasErrors        = errors.New("the plan has errors")
	errRevisionMoved        = errors.New("the environment changed since the plan was computed")
	errConfirmationRequired = errors.New("the plan has operations that need a confirmation")
)

// Apply re-plans the file against the environment and performs the plan's operations in one
// transaction. It needs workspace write, and resource admin on every service the plan deletes.
func (s *PlanServer) Apply(
	ctx context.Context,
	req *connect.Request[planv1.ApplyRequest],
) (*connect.Response[planv1.ApplyResponse], error) {
	r := req.Msg
	planned, err := s.loadPlan(ctx, r.GetFile(), r.GetEnvironmentId(), actions.Apply)
	if err != nil {
		return nil, err
	}
	plan := planned.plan
	if len(plan.Errors) > 0 {
		return nil, applyRefused(errPlanHasErrors, &planv1.ApplyRefusal{Errors: planErrorsToProto(plan.Errors)})
	}
	if planned.env.Revision != r.GetRevision() {
		return nil, applyRefused(errRevisionMoved, &planv1.ApplyRefusal{Revision: planned.env.Revision})
	}
	unconfirmed := unconfirmedOperations(plan.Operations, r.GetConfirmDestructive(), r.GetConfirmImport())
	if len(unconfirmed) > 0 {
		refusal := &planv1.ApplyRefusal{Unconfirmed: planOperationsToProto(unconfirmed)}
		return nil, applyRefused(errConfirmationRequired, refusal)
	}
	if deleteErr := s.checkDeletes(ctx, planned); deleteErr != nil {
		return nil, deleteErr
	}

	a := &applier{
		env:             planned.env,
		partial:         planned.file.Partial,
		live:            planned.live,
		platformDomains: planned.platformDomains,
		defaults:        s.defaults,
	}
	var revision int64
	err = withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		locked, lockErr := qtx.LockEnvironmentRevision(ctx, planned.env.ID)
		if lockErr != nil {
			return lockErr
		}
		if locked != r.GetRevision() {
			return errRevisionMoved
		}
		for _, op := range plan.Operations {
			if opErr := a.apply(ctx, qtx, op); opErr != nil {
				return opErr
			}
		}
		applied, getErr := qtx.GetEnvironmentByID(ctx, planned.env.ID)
		if getErr != nil {
			return getErr
		}
		revision = applied.Revision
		return nil
	})
	if errors.Is(err, errRevisionMoved) {
		current, getErr := s.queries.GetEnvironmentByID(ctx, planned.env.ID)
		if getErr != nil {
			slog.ErrorContext(ctx, "failed to get environment", "error", getErr, "environmentId", planned.env.ID)
			return nil, connect.NewError(connect.CodeInternal, ErrDB)
		}
		return nil, applyRefused(errRevisionMoved, &planv1.ApplyRefusal{Revision: current.Revision})
	}
	if err != nil {
		return nil, applyTxError(ctx, err)
	}

	return connect.NewResponse(&planv1.ApplyResponse{
		Revision:    revision,
		Operations:  planOperationsToProto(plan.Operations),
		Deployments: a.started,
	}), nil
}

func (s *PlanServer) checkDeletes(ctx context.Context, planned *plannedFile) error {
	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return connect.NewError(connect.CodeInternal, errEntityScopesNotFound)
	}
	for _, op := range planned.plan.Operations {
		if op.Kind != configplan.KindDelete {
			continue
		}
		resourceID := planned.live.resources[op.Service].ID.String()
		deleteAction := actions.New(actions.DeleteResource, resourceID)
		if err := s.authz.Check(ctx, scopes, deleteAction); err != nil {
			slog.WarnContext(ctx, "unauthorized to delete resource through apply", "resourceId", resourceID)
			return connect.NewError(connect.CodePermissionDenied, err)
		}
	}
	return nil
}

func unconfirmedOperations(
	operations []configplan.Operation,
	confirmDestructive bool,
	confirmImport bool,
) []configplan.Operation {
	var unconfirmed []configplan.Operation
	for _, op := range operations {
		if op.Destructive && !confirmDestructive {
			unconfirmed = append(unconfirmed, op)
			continue
		}
		if op.Kind == configplan.KindImport && !confirmImport {
			unconfirmed = append(unconfirmed, op)
		}
	}
	return unconfirmed
}

func applyRefused(reason error, refusal *planv1.ApplyRefusal) error {
	refused := connect.NewError(connect.CodeFailedPrecondition, reason)
	detail, err := connect.NewErrorDetail(refusal)
	if err != nil {
		return refused
	}
	refused.AddDetail(detail)
	return refused
}

func applyTxError(ctx context.Context, err error) error {
	if connectErr, isConnect := errors.AsType[*connect.Error](err); isConnect {
		return connectErr
	}
	if errors.Is(err, errNoActiveCluster) {
		slog.WarnContext(ctx, "apply targets a region without a healthy cluster", "error", err)
		return connect.NewError(connect.CodeFailedPrecondition, err)
	}
	if isPgConstraintViolation(err) {
		slog.WarnContext(ctx, "apply rejected by a constraint", "error", err)
		return connect.NewError(connect.CodeAlreadyExists, errDomainInUse)
	}
	return deploymentTxError(ctx, err)
}
