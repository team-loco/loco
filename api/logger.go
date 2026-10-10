package main

import (
	"context"
	"log/slog"

	"github.com/team-loco/loco/api/contextkeys"
)

var redactedLogKeys = map[string]struct{}{
	"env":      {},
	"values":   {},
	"data":     {},
	"secret":   {},
	"token":    {},
	"password": {},
}

type CustomHandler struct {
	slog.Handler
}

func (l CustomHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	kept := make([]slog.Attr, 0, len(attrs))
	for _, attr := range attrs {
		if _, drop := redactedLogKeys[attr.Key]; !drop {
			kept = append(kept, attr)
		}
	}
	return CustomHandler{Handler: l.Handler.WithAttrs(kept)}
}

func (l CustomHandler) WithGroup(name string) slog.Handler {
	return CustomHandler{Handler: l.Handler.WithGroup(name)}
}

func (l CustomHandler) Handle(ctx context.Context, r slog.Record) error {
	clean := redact(r)
	if ctx.Value(contextkeys.RequestIDKey) == nil {
		return l.Handler.Handle(ctx, clean)
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

	clean.AddAttrs(requestGroup)

	return l.Handler.Handle(ctx, clean)
}

func redact(r slog.Record) slog.Record {
	clean := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
	r.Attrs(func(attr slog.Attr) bool {
		if _, drop := redactedLogKeys[attr.Key]; !drop {
			clean.AddAttrs(attr)
		}
		return true
	})
	return clean
}
