package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/team-loco/loco/api/authz/actions"
	"github.com/team-loco/loco/api/contextkeys"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
)

var errPartialUnchanged = errors.New("resource already belongs to this partial")

// TransferPartial moves a resource to another partial.
func (s *ResourceServer) TransferPartial(
	ctx context.Context,
	req *connect.Request[resourcev1.TransferPartialRequest],
) (*connect.Response[resourcev1.TransferPartialResponse], error) {
	r := req.Msg

	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, connect.NewError(connect.CodeInternal, errEntityScopesNotFound)
	}

	if err := s.authz.Check(
		ctx,
		scopes,
		actions.New(actions.TransferPartial, r.GetResourceId()),
	); err != nil {
		slog.WarnContext(ctx, "unauthorized to transfer partial", "resourceId", r.GetResourceId())
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}

	resourceID := uuid.MustParse(r.GetResourceId())
	partial := r.GetPartial()

	err := withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		if lockErr := lockResourceEnvironments(ctx, qtx, resourceID); lockErr != nil {
			return lockErr
		}
		if lockErr := lockResource(ctx, qtx, resourceID); lockErr != nil {
			return lockErr
		}
		res, getErr := qtx.GetResourceByID(ctx, resourceID)
		if getErr != nil {
			return fmt.Errorf("get resource: %w", getErr)
		}
		previous := derefString(res.Partial)
		if previous == partial {
			return errPartialUnchanged
		}
		if setErr := qtx.SetResourcePartial(ctx, genDb.SetResourcePartialParams{
			ID:      resourceID,
			Partial: &partial,
		}); setErr != nil {
			return fmt.Errorf("set resource partial: %w", setErr)
		}
		if bumpErr := bumpResourceEnvironmentRevisions(ctx, qtx, resourceID); bumpErr != nil {
			return bumpErr
		}
		return events.Record(ctx, qtx, events.Event{
			Type:        events.ResourcePartial,
			WorkspaceID: new(res.WorkspaceID),
			SubjectType: events.SubjectResource,
			SubjectID:   new(resourceID),
			Data:        map[string]any{events.FieldPartial: partial, events.FieldFrom: previous},
		})
	})
	if errors.Is(err, ErrResourceNotFound) {
		return nil, connect.NewError(connect.CodeNotFound, ErrResourceNotFound)
	}
	if errors.Is(err, errPartialUnchanged) {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errPartialUnchanged)
	}
	if err != nil {
		return nil, txError(ctx, "failed to transfer partial", err)
	}

	return connect.NewResponse(&resourcev1.TransferPartialResponse{}), nil
}
