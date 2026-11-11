package admission

import (
	"context"
	"errors"
	"math"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	db "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/cache"
	"github.com/team-loco/loco/api/reqctx"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

func (l *Limiter) Interceptor() connect.ServerInterceptor {
	return func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
			return next(l.Context(ctx), spec, &requestStream{ServerStream: stream, ctx: ctx, limiter: l})
		}
	}
}

type requestStream struct {
	connect.ServerStream
	ctx     context.Context
	limiter *Limiter
	charged bool
}

func (s *requestStream) Receive(message any) error {
	if err := s.ServerStream.Receive(message); err != nil {
		return err
	}
	if s.charged {
		return nil
	}
	s.charged = true
	request, ok := message.(proto.Message)
	if !ok {
		panic("admission received a non-protobuf request")
	}
	return s.limiter.admitRequest(s.ctx, request)
}

func (l *Limiter) admitRequest(ctx context.Context, message proto.Message) error {
	budgets := make([]cache.Budget, 0, 3)
	if l.config.GlobalRequests > 0 {
		budgets = append(budgets, cache.Budget{Key: l.requestPrefix + "global", Limit: l.config.GlobalRequests})
	}
	p, authenticated := reqctx.PrincipalFrom(ctx)
	if authenticated && l.config.PrincipalRequests > 0 {
		budgets = append(
			budgets,
			cache.Budget{
				Key:   l.requestPrefix + "principal:" + string(p.Entity.Type) + ":" + p.Entity.ID.String(),
				Limit: l.config.PrincipalRequests,
			},
		)
	}
	if authenticated && l.config.WorkspaceRequests > 0 {
		target, err := l.workspace(ctx, message.ProtoReflect())
		if err != nil {
			if connect.CodeOf(err) == connect.CodeInvalidArgument {
				return err
			}
			return exhausted(ctx, int64(math.Ceil(l.config.Window.Seconds()))).WithCause(err)
		}
		if target.WorkspaceID != uuid.Nil && visible(p, target) {
			budgets = append(
				budgets,
				cache.Budget{
					Key:   l.requestPrefix + "workspace:" + target.WorkspaceID.String(),
					Limit: l.config.WorkspaceRequests,
				},
			)
		}
	}
	if len(budgets) == 0 {
		return nil
	}
	delay, err := l.cache.Admit(ctx, budgets, l.config.Window)
	if err != nil {
		return exhausted(ctx, int64(math.Ceil(l.config.Window.Seconds()))).WithCause(err)
	}
	if delay > 0 {
		return exhausted(ctx, int64(math.Ceil(delay.Seconds())))
	}
	return nil
}

func (l *Limiter) workspace(ctx context.Context, message protoreflect.Message) (db.GetAdmissionTargetRow, error) {
	for _, name := range []protoreflect.Name{
		"resource_id", "workspace_id", "environment_id", "build_id", "deployment_id", "domain_id",
	} {
		field := message.Descriptor().Fields().ByName(name)
		if field == nil || message.Get(field).String() == "" {
			continue
		}
		id, err := uuid.Parse(message.Get(field).String())
		if err != nil {
			return db.GetAdmissionTargetRow{}, connect.NewError(connect.CodeInvalidArgument, "invalid "+string(name)).
				WithCause(err)
		}
		p, _ := reqctx.PrincipalFrom(ctx)
		var resources []uuid.UUID
		for _, scope := range p.Scopes {
			if scope.EntityType == db.EntityTypeResource {
				resources = append(resources, scope.EntityID)
			}
		}
		target, err := db.New(l.pool).
			GetAdmissionTarget(ctx, db.GetAdmissionTargetParams{Kind: string(name), ID: id, ResourceIds: resources})
		if errors.Is(err, pgx.ErrNoRows) {
			return db.GetAdmissionTargetRow{}, nil
		}
		if err != nil {
			return db.GetAdmissionTargetRow{}, err
		}
		return target, nil
	}
	if field := message.Descriptor().Fields().ByName("name_key"); field != nil && message.Has(field) {
		return l.workspace(ctx, message.Get(field).Message())
	}
	return db.GetAdmissionTargetRow{}, nil
}

func visible(p reqctx.Principal, target db.GetAdmissionTargetRow) bool {
	if target.ResourceGrant {
		return true
	}
	for _, scope := range p.Scopes {
		switch scope.EntityType {
		case db.EntityTypeSystem:
			return true
		case db.EntityTypeOrganization:
			if scope.EntityID == target.OrgID {
				return true
			}
		case db.EntityTypeWorkspace:
			if scope.EntityID == target.WorkspaceID {
				return true
			}
		}
	}
	return false
}
