package interceptor

import (
	"context"
	"fmt"
	"sync"

	"connectrpc.com/connect/v2"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/reqctx"
	"github.com/team-loco/loco/internal/rpcerr"
)

const (
	streamMethodAttr = "rpc.method"
	streamLimitAttr  = "loco.stream.limit"
)

var (
	errCallerStreamLimit  = rpcerr.NewCoded(connect.CodeResourceExhausted, "too many open streams for this caller")
	errReplicaStreamLimit = rpcerr.NewCoded(connect.CodeResourceExhausted, "the server has too many open streams")
)

// StreamLimits caps the server streams one API replica holds open, in all and per principal.
type StreamLimits struct {
	PerReplica   int32
	PerPrincipal int32
}

type streamLimiter struct {
	limits    StreamLimits
	openGauge metric.Int64UpDownCounter
	refusals  metric.Int64Counter

	mu          sync.Mutex
	open        int32
	byPrincipal map[genDb.Entity]int32
}

// NewStreamLimiter refuses a server stream with ResourceExhausted once its replica or its
// principal holds the most streams limits allow, and records open and refused streams on meter.
func NewStreamLimiter(limits StreamLimits, meter metric.Meter) (*streamLimiter, error) {
	openGauge, err := meter.Int64UpDownCounter("loco.api.streams.open",
		metric.WithDescription("Server streams this API replica holds open."))
	if err != nil {
		return nil, fmt.Errorf("open streams instrument: %w", err)
	}
	refusals, err := meter.Int64Counter("loco.api.streams.refused",
		metric.WithDescription("Server streams this API replica refused at a stream limit."))
	if err != nil {
		return nil, fmt.Errorf("refused streams instrument: %w", err)
	}
	return &streamLimiter{
		limits:      limits,
		openGauge:   openGauge,
		refusals:    refusals,
		byPrincipal: make(map[genDb.Entity]int32),
	}, nil
}

func (l *streamLimiter) Interceptor() connect.ServerInterceptor {
	return func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
			principal, ok := reqctx.PrincipalFrom(ctx)
			if spec.StreamType != connect.StreamTypeServer || !ok {
				return next(ctx, spec, stream)
			}
			method := attribute.String(streamMethodAttr, spec.Procedure)
			if limit, err := l.acquire(principal.Entity); err != nil {
				l.refusals.Add(ctx, 1, metric.WithAttributes(method, attribute.String(streamLimitAttr, limit)))
				return err
			}
			l.openGauge.Add(ctx, 1, metric.WithAttributes(method))
			defer func() {
				l.release(principal.Entity)
				l.openGauge.Add(context.WithoutCancel(ctx), -1, metric.WithAttributes(method))
			}()
			return next(ctx, spec, stream)
		}
	}
}

// acquire takes a stream slot for entity, or names the limit that refuses it.
func (l *streamLimiter) acquire(entity genDb.Entity) (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.open >= l.limits.PerReplica {
		return "replica", errReplicaStreamLimit
	}
	if l.byPrincipal[entity] >= l.limits.PerPrincipal {
		return "principal", errCallerStreamLimit
	}
	l.open++
	l.byPrincipal[entity]++
	return "", nil
}

func (l *streamLimiter) release(entity genDb.Entity) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.open--
	l.byPrincipal[entity]--
	if l.byPrincipal[entity] == 0 {
		delete(l.byPrincipal, entity)
	}
}
