package telemetry

import (
	"strings"
	"testing"
	"time"
)

func TestCollectorCrashWindow(t *testing.T) {
	started := time.Now()
	for _, tt := range []struct {
		name         string
		acknowledged time.Time
		killed       time.Time
		valid        bool
	}{
		{"delayed startup", started.Add(43 * time.Second), started.Add(44 * time.Second), true},
		{"before flush", started.Add(crashFlush - 2*time.Second), started.Add(crashFlush - time.Second), true},
		{"at flush", started.Add(time.Second), started.Add(crashFlush), false},
		{"after flush", started.Add(time.Second), started.Add(crashFlush + time.Second), false},
		{"no acknowledgment", time.Time{}, started.Add(time.Second), false},
		{"no kill", started.Add(time.Second), time.Time{}, false},
		{"kill before acknowledgment", started.Add(2 * time.Second), started.Add(time.Second), false},
		{"acknowledgment before startup", started.Add(-time.Second), started.Add(time.Second), false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := collectorCrashWindow(started, tt.acknowledged, tt.killed, crashFlush)
			if tt.valid {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil {
				t.Fatal("invalid experiment accepted")
			}
			for _, want := range []string{"invalid collector crash experiment", "acknowledgment=", "SIGKILL=", "flush="} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q omits %q", err, want)
				}
			}
		})
	}
}
