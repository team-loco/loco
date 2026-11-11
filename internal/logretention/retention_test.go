package logretention

import (
	"testing"
	"time"
)

func TestResolve(t *testing.T) {
	maximum := 30 * 24 * time.Hour
	for value, want := range map[string]time.Duration{
		"":    maximum,
		"7d":  7 * 24 * time.Hour,
		"24h": 24 * time.Hour,
		"1s":  time.Second,
		"30d": maximum,
	} {
		got, err := Resolve(value, maximum)
		if err != nil || got != want {
			t.Fatalf("Resolve(%q) = %v, %v", value, got, err)
		}
	}
	for _, value := range []string{"31d", "0s", "-1d", "1.5s", "bad", "999999999999d", "-999999999999d"} {
		if _, err := Resolve(value, maximum); err == nil {
			t.Errorf("Resolve(%q) succeeded", value)
		}
	}
}

func TestMaximum(t *testing.T) {
	t.Setenv("CLICKHOUSE_LOGS_TTL", "")
	if got := Maximum(); got != 365*24*time.Hour {
		t.Fatalf("default = %v", got)
	}
	t.Setenv("CLICKHOUSE_LOGS_TTL", "48h")
	if got := Maximum(); got != 48*time.Hour {
		t.Fatalf("configured = %v", got)
	}
	for _, value := range []string{"0s", "-1h", "1.5s", "bad"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("CLICKHOUSE_LOGS_TTL", value)
			defer func() {
				if recover() == nil {
					t.Fatal("expected invalid startup configuration to panic")
				}
			}()
			Maximum()
		})
	}
}
