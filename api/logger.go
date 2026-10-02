package main

import (
	"context"
	"log/slog"

	"github.com/team-loco/loco/api/contextkeys"
)

type CustomHandler struct {
	slog.Handler
}

func (l CustomHandler) Handle(ctx context.Context, r slog.Record) error {
	if ctx.Value(contextkeys.RequestIDKey) == nil {
		return l.Handler.Handle(ctx, r)
	}

	requestID, okReqID := ctx.Value(contextkeys.RequestIDKey).(string)
	if !okReqID {
		requestID = ""
	}
	sourceIP, okSourceIP := ctx.Value(contextkeys.SourceIPKey).(string)
	if !okSourceIP {
		sourceIP = ""
	}
	path, okPath := ctx.Value(contextkeys.PathKey).(string)
	if !okPath {
		path = ""
	}
	method, okMethod := ctx.Value(contextkeys.MethodKey).(string)
	if !okMethod {
		method = ""
	}

	// can be null on routes where oAuth Middleware is skipped.
	entity := ctx.Value(contextkeys.EntityKey)

	requestGroup := slog.Group(
		"request",
		slog.String("requestId", requestID),
		slog.String("sourceIp", sourceIP),
		slog.String("method", method),
		slog.String("path", path),
		slog.Any("entity", entity),
	)

	r.AddAttrs(requestGroup)

	return l.Handler.Handle(ctx, r)
}
