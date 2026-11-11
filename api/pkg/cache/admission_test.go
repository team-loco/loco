package cache

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestAdmissionCountersAreAtomicAndExpire(t *testing.T) {
	for name, backend := range testCaches(t) {
		t.Run(name, func(t *testing.T) {
			prefix := "{test-counters}:" + uuid.NewString()
			global := Budget{Key: prefix + ":global", Limit: 2}
			neighbor := Budget{Key: prefix + ":neighbor", Limit: 1}
			caller := Budget{Key: prefix + ":caller", Limit: 1}
			window := 20 * time.Millisecond
			wins := countConcurrent(20, func() bool {
				delay, err := backend.Admit(t.Context(), []Budget{global, neighbor}, time.Hour)
				if err != nil {
					t.Error(err)
					return false
				}
				return delay == 0
			})
			if wins != 1 {
				t.Fatalf("admitted %d", wins)
			}
			if delay, err := backend.Admit(t.Context(), []Budget{global, caller}, time.Hour); err != nil || delay != 0 {
				t.Fatalf("neighbor spent global capacity: %v %v", delay, err)
			}
			expiring := []Budget{{Key: prefix + ":expiry", Limit: 1}}
			if delay, err := backend.Admit(t.Context(), expiring, window); err != nil || delay != 0 {
				t.Fatalf("first: %v %v", delay, err)
			}
			delay, err := backend.Admit(t.Context(), expiring, window)
			if err != nil || delay <= 0 {
				t.Fatalf("throttled: %v %v", delay, err)
			}
			time.Sleep(delay + time.Millisecond)
			if delay, err := backend.Admit(t.Context(), expiring, window); err != nil || delay != 0 {
				t.Fatalf("expired: %v %v", delay, err)
			}
		})
	}
}

func BenchmarkRequestAdmission(b *testing.B) {
	backend, err := NewMemory()
	if err != nil {
		b.Fatal(err)
	}
	defer backend.Close()
	budgets := []Budget{
		{Key: "global", Limit: 2147483647},
		{Key: "principal", Limit: 2147483647},
		{Key: "workspace", Limit: 2147483647},
	}
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if delay, err := backend.Admit(context.Background(), budgets, time.Hour); err != nil || delay != 0 {
				b.Errorf("admit: %v %v", delay, err)
			}
		}
	})
}
