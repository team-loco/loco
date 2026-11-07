package service

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect/v2"

	"github.com/team-loco/loco/api/authz/actions"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	"github.com/team-loco/loco/internal/rpcerr"
)

var errPartialUnchanged = errors.New("resource already belongs to this partial")

// TransferPartial moves a resource to another partial.
func (s *ResourceServer) TransferPartial(
	ctx context.Context,
	req *resourcev1.TransferPartialRequest,
) (*resourcev1.TransferPartialResponse, error) {
	resourceID, err := parseID("resource_id", req.GetResourceId())
	if err != nil {
		return nil, err
	}
	if err = authorize(ctx, s.authz, actions.TransferPartial, resourceID); err != nil {
		return nil, err
	}

	partial := req.GetPartial()

	err = withTx(ctx, s.db, func(qtx *genDb.Queries) error {
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
		return nil, rpcerr.New(connect.CodeNotFound, ErrResourceNotFound)
	}
	if errors.Is(err, errPartialUnchanged) {
		return nil, rpcerr.New(connect.CodeFailedPrecondition, errPartialUnchanged)
	}
	if err != nil {
		return nil, txError(ctx, "failed to transfer partial", err)
	}

	return &resourcev1.TransferPartialResponse{}, nil
}
