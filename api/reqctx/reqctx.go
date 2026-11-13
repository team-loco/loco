// Package reqctx carries the values the request interceptors attach to a context: the request
// ID and procedure, and the authenticated principal.
package reqctx

import (
	"context"

	"github.com/google/uuid"

	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/internal/rpc"
)

type key int

const (
	requestKey key = iota
	principalKey
)

// Request identifies one RPC call.
type Request struct {
	ID        string
	Procedure string
}

// Principal is the caller the auth interceptor authenticated.
type Principal struct {
	Entity    genDb.Entity
	Scopes    []genDb.EntityScope
	ActorType genDb.DeploymentActorType
	ActorID   uuid.UUID
	Method    genDb.DeploymentMethod
	Token     string
}

// WithRequest keeps the ID in rpc's context value, so outgoing RPC clients forward it.
func WithRequest(ctx context.Context, r Request) context.Context {
	ctx = rpc.WithRequestID(ctx, r.ID)
	return context.WithValue(ctx, requestKey, r.Procedure)
}

func RequestFrom(ctx context.Context) (Request, bool) {
	procedure, ok := ctx.Value(requestKey).(string)
	id, _ := rpc.RequestIDFrom(ctx)
	return Request{ID: id, Procedure: procedure}, ok
}

func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey, p)
}

// PrincipalFrom reports false on public procedures and outside RPC handlers.
func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey).(Principal)
	return p, ok
}
