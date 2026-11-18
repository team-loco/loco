package main

import (
	"errors"
	"testing"
	"time"

	"github.com/team-loco/loco/api/auth"
	"github.com/team-loco/loco/internal/envconfig"
)

func TestNewAPIConfigReadsStreamAuthentication(t *testing.T) {
	clearAPIConfigEnv(t)
	config := newAPIConfig()
	if config.StreamAuth != (auth.StreamConfig{Interval: 5 * time.Second, Timeout: 2 * time.Second}) {
		t.Fatalf("stream authentication defaults = %+v", config.StreamAuth)
	}
	t.Setenv("LOCO_STREAM_AUTH_INTERVAL", "3s")
	t.Setenv("LOCO_STREAM_AUTH_TIMEOUT", "1s")
	config = newAPIConfig()
	if config.StreamAuth != (auth.StreamConfig{Interval: 3 * time.Second, Timeout: time.Second}) {
		t.Fatalf("configured stream authentication = %+v", config.StreamAuth)
	}
}

func TestNewAPIConfigRejectsInvalidStreamAuthentication(t *testing.T) {
	for _, name := range []string{"LOCO_STREAM_AUTH_INTERVAL", "LOCO_STREAM_AUTH_TIMEOUT"} {
		for _, raw := range []string{"0s", "-1s", "not-a-duration"} {
			t.Run(name+"/"+raw, func(t *testing.T) {
				clearAPIConfigEnv(t)
				t.Setenv(name, raw)
				want := envconfig.ErrNotPositive
				if raw == "not-a-duration" {
					want = envconfig.ErrInvalidDuration
				}
				if err := panicValue(t, func() { newAPIConfig() }); !errors.Is(err, want) {
					t.Fatalf("invalid stream authentication panic = %v, want %v", err, want)
				}
			})
		}
	}
}

func TestNewAPIConfigRejectsStreamTimeoutAboveInterval(t *testing.T) {
	clearAPIConfigEnv(t)
	t.Setenv("LOCO_STREAM_AUTH_INTERVAL", "1s")
	t.Setenv("LOCO_STREAM_AUTH_TIMEOUT", "2s")
	if err := panicValue(t, func() { newAPIConfig() }); !errors.Is(err, errStreamAuthTimeout) {
		t.Fatalf("stream timeout above interval panic = %v, want %v", err, errStreamAuthTimeout)
	}
	t.Setenv("LOCO_STREAM_AUTH_TIMEOUT", "1s")
	if config := newAPIConfig(); config.StreamAuth != (auth.StreamConfig{Interval: time.Second, Timeout: time.Second}) {
		t.Fatalf("stream timeout equal to interval = %+v", config.StreamAuth)
	}
}
