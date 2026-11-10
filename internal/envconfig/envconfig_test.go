package envconfig

import (
	"errors"
	"testing"
	"time"
)

const (
	testEnvName     = "LOCO_TEST_ENV_VALUE"
	testEnvFallback = 7
)

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

func TestString(t *testing.T) {
	t.Setenv(testEnvName, "")
	if got := String(testEnvName, "fallback"); got != "fallback" {
		t.Errorf("unset = %q, want the fallback", got)
	}
	t.Setenv(testEnvName, "value")
	if got := String(testEnvName, "fallback"); got != "value" {
		t.Errorf("set = %q, want value", got)
	}
}

func TestBool(t *testing.T) {
	t.Setenv(testEnvName, "")
	if !Bool(testEnvName, true) {
		t.Error("unset did not return the fallback")
	}
	t.Setenv(testEnvName, "false")
	if Bool(testEnvName, true) {
		t.Error("false parsed as true")
	}
	t.Setenv(testEnvName, "sometimes")
	err := panicValue(t, func() { Bool(testEnvName, true) })
	if !errors.Is(err, ErrInvalidBool) {
		t.Errorf("panic = %v, want %v", err, ErrInvalidBool)
	}
}

func TestInt32(t *testing.T) {
	t.Setenv(testEnvName, "")
	if got := Int32(testEnvName, testEnvFallback); got != testEnvFallback {
		t.Errorf("unset = %d, want the fallback", got)
	}
	t.Setenv(testEnvName, "-3")
	if got := Int32(testEnvName, testEnvFallback); got != -3 {
		t.Errorf("set = %d, want -3", got)
	}
	for _, value := range []string{"4294967296", "many"} {
		t.Setenv(testEnvName, value)
		err := panicValue(t, func() { Int32(testEnvName, testEnvFallback) })
		if !errors.Is(err, ErrInvalidInt32) {
			t.Errorf("%q panic = %v, want %v", value, err, ErrInvalidInt32)
		}
	}
}

func TestPositiveDuration(t *testing.T) {
	read := func() { PositiveDuration(testEnvName, time.Minute) }
	t.Setenv(testEnvName, "")
	if got := PositiveDuration(testEnvName, time.Minute); got != time.Minute {
		t.Errorf("unset = %s, want the fallback", got)
	}
	t.Setenv(testEnvName, "90s")
	if got := PositiveDuration(testEnvName, time.Minute); got != 90*time.Second {
		t.Errorf("set = %s, want 90s", got)
	}
	t.Setenv(testEnvName, "90")
	if err := panicValue(t, read); !errors.Is(err, ErrInvalidDuration) {
		t.Errorf("panic = %v, want %v", err, ErrInvalidDuration)
	}
	t.Setenv(testEnvName, "-1s")
	if err := panicValue(t, read); !errors.Is(err, ErrNotPositive) {
		t.Errorf("panic = %v, want %v", err, ErrNotPositive)
	}
}
