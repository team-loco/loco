// Package reqctx carries the values the request interceptors attach to a context: the request
// ID and procedure, and the authenticated principal.
package reqctx

import (
	"context"
	"sync/atomic"

	"github.com/google/uuid"

	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/internal/rpc"
)

type key int

const (
	requestKey key = iota
	principalKey
	operatorKey
)

// Request identifies one RPC call.
type Request struct {
	ID        string
	Procedure string
}

// Principal is the caller the auth interceptor authenticated.
type Principal struct {
	// Entity is the credential scope owner; API-token identity is ActorID.
	Entity    genDb.Entity
	Scopes    []genDb.EntityScope
	ActorType genDb.DeploymentActorType
	ActorID   uuid.UUID
	// UserID is the human principal for a session or personal token, and Nil for an owned token.
	UserID uuid.UUID
	// SessionID is set only for a Loco session.
	SessionID uuid.UUID
	// Method identifies dashboard, CLI, and API token callers.
	Method genDb.DeploymentMethod
	Token  string
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

// WithPrincipal attaches p. The request's operator mark is kept when ctx already carries one.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	if _, ok := ctx.Value(operatorKey).(*atomic.Bool); !ok {
		ctx = context.WithValue(ctx, operatorKey, new(atomic.Bool))
	}
	return context.WithValue(ctx, principalKey, p)
}

// MarkOperator records that the request passed an access check only through an installation role.
func MarkOperator(ctx context.Context) {
	if mark, ok := ctx.Value(operatorKey).(*atomic.Bool); ok {
		mark.Store(true)
	}
}

// ActedAsOperator reports whether the request passed an access check only through an installation role.
func ActedAsOperator(ctx context.Context) bool {
	mark, ok := ctx.Value(operatorKey).(*atomic.Bool)
	return ok && mark.Load()
}

// PrincipalFrom reports false on public procedures and outside RPC handlers.
func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey).(Principal)
	return p, ok
}
