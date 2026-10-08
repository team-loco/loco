package main

import (
	"errors"
	"log/slog"
	"slices"
	"testing"
	"time"
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

func TestStringEnv(t *testing.T) {
	t.Setenv(testEnvName, "")
	if got := stringEnv(testEnvName, "fallback"); got != "fallback" {
		t.Errorf("unset = %q, want the fallback", got)
	}
	t.Setenv(testEnvName, "value")
	if got := stringEnv(testEnvName, "fallback"); got != "value" {
		t.Errorf("set = %q, want value", got)
	}
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

func TestBoolEnv(t *testing.T) {
	t.Setenv(testEnvName, "")
	if !boolEnv(testEnvName, true, errTestInvalid) {
		t.Error("unset did not return the fallback")
	}
	t.Setenv(testEnvName, "false")
	if boolEnv(testEnvName, true, errTestInvalid) {
		t.Error("false parsed as true")
	}
	t.Setenv(testEnvName, "sometimes")
	err := panicValue(t, func() { boolEnv(testEnvName, true, errTestInvalid) })
	if !errors.Is(err, errTestInvalid) {
		t.Errorf("panic = %v, want %v", err, errTestInvalid)
	}
}

func readInt32()            { int32Env(testEnvName, testEnvFallback) }
func readPositiveInt32()    { positiveInt32Env(testEnvName, testEnvFallback) }
func readNonNegativeInt32() { nonNegativeInt32Env(testEnvName, testEnvFallback) }
func readPositiveInt64()    { positiveInt64Env(testEnvName, testEnvFallback, errTestInvalid) }

func TestIntegerEnvs(t *testing.T) {
	t.Setenv(testEnvName, "")
	if got := int32Env(testEnvName, testEnvFallback); got != testEnvFallback {
		t.Errorf("unset = %d, want the fallback", got)
	}
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
		{"int32 overflow", "4294967296", readInt32, errInvalidInt32},
		{"positive int32 zero", "0", readPositiveInt32, errNotPositive},
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

func readPositiveDuration() { positiveDurationEnv(testEnvName, time.Minute) }

func TestPositiveDurationEnv(t *testing.T) {
	t.Setenv(testEnvName, "")
	if got := positiveDurationEnv(testEnvName, time.Minute); got != time.Minute {
		t.Errorf("unset = %s, want the fallback", got)
	}
	t.Setenv(testEnvName, "90s")
	if got := positiveDurationEnv(testEnvName, time.Minute); got != 90*time.Second {
		t.Errorf("set = %s, want 90s", got)
	}
	t.Setenv(testEnvName, "90")
	if err := panicValue(t, readPositiveDuration); !errors.Is(err, errInvalidDuration) {
		t.Errorf("panic = %v, want %v", err, errInvalidDuration)
	}
	t.Setenv(testEnvName, "-1s")
	if err := panicValue(t, readPositiveDuration); !errors.Is(err, errNotPositive) {
		t.Errorf("panic = %v, want %v", err, errNotPositive)
	}
}

func TestLogLevelEnv(t *testing.T) {
	t.Setenv(testEnvName, "")
	if _, ok := logLevelEnv(testEnvName); ok {
		t.Error("unset reported a level")
	}
	t.Setenv(testEnvName, "-4")
	if got, ok := logLevelEnv(testEnvName); !ok || got != slog.LevelDebug {
		t.Errorf("-4 = %v, %v, want debug", got, ok)
	}
	t.Setenv(testEnvName, "loud")
	if _, ok := logLevelEnv(testEnvName); ok {
		t.Error("non-numeric reported a level")
	}
}
