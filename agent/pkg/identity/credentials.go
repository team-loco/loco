package identity

import (
	"context"
	"sync/atomic"
	"time"

	"connectrpc.com/connect/v2"
)

type Credentials struct {
	enrolled atomic.Pointer[Identity]
	lifetime time.Duration
}

func NewCredentials(lifetime time.Duration) *Credentials {
	return &Credentials{lifetime: lifetime}
}

func (c *Credentials) Use(identity Identity) {
	c.enrolled.Store(&identity)
}

func (c *Credentials) Interceptor() connect.ClientInterceptor {
	return func(next connect.ClientFunc) connect.ClientFunc {
		return func(ctx context.Context, spec connect.Spec) (connect.ClientStream, error) {
			enrolled := c.enrolled.Load()
			if enrolled == nil {
				return next(ctx, spec)
			}
			assertion, err := enrolled.Assertion(time.Now(), c.lifetime)
			if err != nil {
				return nil, err
			}
			info, ok := connect.CallInfoForClientContext(ctx)
			if !ok {
				ctx, info = connect.NewClientContext(ctx)
			}
			info.RequestHeader().Set("Authorization", "Bearer "+assertion)
			return next(ctx, spec)
		}
	}
}
