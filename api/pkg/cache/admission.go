package cache

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/valkey-io/valkey-go"
)

type Budget struct {
	Key   string
	Limit int32
}

type counter struct {
	used    int32
	expires time.Time
}

// Admit checks all budgets before incrementing so a throttled neighbor cannot consume shared capacity.
func (m *MemoryCache) Admit(_ context.Context, budgets []Budget, window time.Duration) (time.Duration, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	if !now.Before(m.counterCleanup) {
		for key, value := range m.counters {
			if !now.Before(value.expires) {
				delete(m.counters, key)
			}
		}
		m.counterCleanup = now.Add(window)
	}
	for _, b := range budgets {
		value := m.counters[b.Key]
		if now.Before(value.expires) && value.used >= b.Limit {
			return value.expires.Sub(now), nil
		}
	}
	for _, b := range budgets {
		value := m.counters[b.Key]
		if !now.Before(value.expires) {
			value = counter{expires: now.Add(window)}
		}
		value.used++
		m.counters[b.Key] = value
	}
	return 0, nil
}

var admitScript = valkey.NewLuaScript(`
for i, key in ipairs(KEYS) do
 local used = tonumber(redis.call('GET', key) or '0')
 if used >= tonumber(ARGV[i + 1]) then
  return math.max(1, redis.call('PTTL', key))
 end
end
for _, key in ipairs(KEYS) do
 if redis.call('INCR', key) == 1 then
  redis.call('PEXPIRE', key, ARGV[1])
 end
end
return 0
`)

func (v *ValkeyAdapter) Admit(ctx context.Context, budgets []Budget, window time.Duration) (time.Duration, error) {
	if len(budgets) == 0 {
		return 0, nil
	}
	keys := make([]string, 0, len(budgets))
	args := []string{strconv.FormatInt(max(1, window.Milliseconds()), 10)}
	for _, b := range budgets {
		keys = append(keys, b.Key)
		args = append(args, strconv.FormatInt(int64(b.Limit), 10))
	}
	milliseconds, err := admitScript.Exec(ctx, v.client, keys, args).ToInt64()
	if err != nil {
		return 0, fmt.Errorf("admit cache budgets: %w", err)
	}
	return time.Duration(milliseconds) * time.Millisecond, nil
}
