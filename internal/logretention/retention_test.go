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

func TestValidateMaximum(t *testing.T) {
	for _, maximum := range []time.Duration{DefaultMaximum, time.Second, 48 * time.Hour} {
		if err := ValidateMaximum(maximum); err != nil {
			t.Fatalf("ValidateMaximum(%v): %v", maximum, err)
		}
	}
	for _, maximum := range []time.Duration{0, -time.Hour, 1500 * time.Millisecond} {
		if err := ValidateMaximum(maximum); err == nil {
			t.Errorf("ValidateMaximum(%v) succeeded", maximum)
		}
	}
}
