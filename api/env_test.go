package main

import (
	"errors"
	"log/slog"
	"slices"
	"testing"

	"github.com/team-loco/loco/internal/envconfig"
	"github.com/team-loco/loco/internal/loglevel"
)

const (
	testEnvName     = "LOCO_TEST_ENV_VALUE"
	testEnvFallback = 7
)

var errTestInvalid = errors.New("test value is invalid")

func panicValue(t *testing.T, fn func()) error {
	t.Helper()
	var recovered any
	func() {
		defer func() { recovered = recover() }()
		fn()
	}()
	if recovered == nil {
		t.Fatal("expected a panic")
	}
	err, ok := recovered.(error)
	if !ok {
		t.Fatalf("panic value %v is not an error", recovered)
	}
	return err
}

func TestListEnv(t *testing.T) {
	t.Setenv(testEnvName, "")
	if got := listEnv(testEnvName); len(got) != 0 {
		t.Errorf("unset = %v, want empty", got)
	}
	t.Setenv(testEnvName, "a, b ,c")
	if got := listEnv(testEnvName); !slices.Equal(got, []string{"a", "b", "c"}) {
		t.Errorf("list = %v, want [a b c]", got)
	}
}

func readPositiveInt32()    { positiveInt32Env(testEnvName, testEnvFallback) }
func readNonNegativeInt32() { nonNegativeInt32Env(testEnvName, testEnvFallback) }
func readPositiveInt64()    { positiveInt64Env(testEnvName, testEnvFallback, errTestInvalid) }

func TestIntegerEnvs(t *testing.T) {
	t.Setenv(testEnvName, "")
	t.Setenv(testEnvName, "0")
	if got := nonNegativeInt32Env(testEnvName, testEnvFallback); got != 0 {
		t.Errorf("non-negative 0 = %d, want 0", got)
	}
	tests := []struct {
		name  string
		value string
		read  func()
		want  error
	}{
		{"positive int32 zero", "0", readPositiveInt32, envconfig.ErrNotPositive},
		{"non-negative int32 below zero", "-1", readNonNegativeInt32, errNegative},
		{"positive int64 zero", "0", readPositiveInt64, errTestInvalid},
		{"positive int64 text", "many", readPositiveInt64, errTestInvalid},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(testEnvName, tt.value)
			if err := panicValue(t, tt.read); !errors.Is(err, tt.want) {
				t.Errorf("panic = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestLogLevelEnv(t *testing.T) {
	t.Setenv(testEnvName, "")
	if _, ok := logLevelEnv(testEnvName); ok {
		t.Error("unset reported a level")
	}
	t.Setenv(testEnvName, "debug")
	if got, ok := logLevelEnv(testEnvName); !ok || got != slog.LevelDebug {
		t.Errorf("debug = %v, %v, want debug", got, ok)
	}
	t.Setenv(testEnvName, "-4")
	defer func() {
		recovered, ok := recover().(error)
		if !ok || !errors.Is(recovered, loglevel.ErrUnknown) {
			t.Errorf("-4 panicked with %v, want loglevel.ErrUnknown", recovered)
		}
	}()
	logLevelEnv(testEnvName)
	t.Error("-4 did not panic")
}
