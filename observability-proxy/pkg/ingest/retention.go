package ingest

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sync"
	"time"

	"connectrpc.com/connect/v2"

	ingestv1 "github.com/team-loco/loco/gen/go/loco/ingest/v1"
	"github.com/team-loco/loco/internal/bearer"
)

// maxRetentionBatch is the most workspace IDs GetRetention accepts in one call.
const maxRetentionBatch = 1000

type fetchRetention func(ctx context.Context, ids []string) (map[string]*ingestv1.Retention, error)

// retentionCache holds each workspace's effective retention for ttl. A lookup fetches the IDs it
// has no fresh entry for in one call, which concurrent lookups of the same IDs wait on.
type retentionCache struct {
	ttl time.Duration

	mu      sync.Mutex
	entries map[string]retentionEntry
	pending map[string]*retentionFetch
}

type retentionEntry struct {
	days      *ingestv1.Retention
	fetchedAt time.Time
}

type retentionFetch struct {
	done chan struct{}
	days map[string]*ingestv1.Retention
	err  error
}

func newRetentionCache(ttl time.Duration) *retentionCache {
	return &retentionCache{
		ttl:     ttl,
		entries: map[string]retentionEntry{},
		pending: map[string]*retentionFetch{},
	}
}

// lookup returns the retention of every workspace in ids, fetching the missing ones with fetch.
func (c *retentionCache) lookup(ctx context.Context, ids []string, fetch fetchRetention) (
	map[string]*ingestv1.Retention, error,
) {
	found := make(map[string]*ingestv1.Retention, len(ids))
	waits := map[*retentionFetch]struct{}{}
	var own *retentionFetch
	var missing []string
	c.mu.Lock()
	for _, id := range ids {
		if entry, ok := c.entries[id]; ok && time.Since(entry.fetchedAt) < c.ttl {
			found[id] = entry.days
			continue
		}
		if shared, ok := c.pending[id]; ok {
			waits[shared] = struct{}{}
			continue
		}
		if own == nil {
			own = &retentionFetch{done: make(chan struct{})}
			waits[own] = struct{}{}
		}
		c.pending[id] = own
		missing = append(missing, id)
	}
	c.mu.Unlock()

	if own != nil {
		// The fetch outlives this caller's cancellation: other lookups wait on it.
		own.days, own.err = fetch(context.WithoutCancel(ctx), missing)
		c.store(missing, own)
	}
	for shared := range waits {
		select {
		case <-shared.done:
		case <-ctx.Done():
			return nil, fmt.Errorf("%w: %w", ErrAuthUnavailable, ctx.Err())
		}
		if shared.err != nil {
			return nil, shared.err
		}
		maps.Copy(found, shared.days)
	}
	for _, id := range ids {
		if found[id] == nil {
			return nil, fmt.Errorf("%w: control plane sent no retention for workspace %s", ErrAuthUnavailable, id)
		}
	}
	return found, nil
}

func (c *retentionCache) store(ids []string, fetched *retentionFetch) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	maps.DeleteFunc(c.entries, func(_ string, entry retentionEntry) bool { return now.Sub(entry.fetchedAt) >= c.ttl })
	for _, id := range ids {
		delete(c.pending, id)
		if days, ok := fetched.days[id]; ok && fetched.err == nil {
			c.entries[id] = retentionEntry{days: days, fetchedAt: now}
		}
	}
	close(fetched.done)
}

// fetchRetention asks the control plane for the effective retention of ids with token.
func (g *Gate) fetchRetention(token string) fetchRetention {
	return func(ctx context.Context, ids []string) (map[string]*ingestv1.Retention, error) {
		days := make(map[string]*ingestv1.Retention, len(ids))
		for batch := range slices.Chunk(ids, maxRetentionBatch) {
			callCtx, info := connect.NewClientContext(ctx)
			info.RequestHeader().Set("Authorization", bearer.Header(token))
			resp, err := g.placements.GetRetention(callCtx, &ingestv1.GetRetentionRequest{WorkspaceIds: batch})
			if err != nil && connect.CodeOf(err) == connect.CodeUnauthenticated {
				return nil, fmt.Errorf("%w: %w", ErrUnauthenticated, err)
			}
			if err != nil {
				return nil, fmt.Errorf("%w: get retention: %w", ErrAuthUnavailable, err)
			}
			for id, retention := range resp.GetRetention() {
				if err = checkRetention(retention); err != nil {
					return nil, err
				}
				days[id] = retention
			}
		}
		return days, nil
	}
}
