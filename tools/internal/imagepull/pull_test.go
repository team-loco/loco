package imagepull

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

const testImage = "registry.test/image:tag@sha256:0"

var errPullFailed = errors.New("exit status 1")

func scriptedPull(outputs ...string) (pullFunc, *int) {
	calls := 0
	return func(context.Context, string) ([]byte, error) {
		output := outputs[calls]
		calls++
		if output == "" {
			return nil, nil
		}
		return []byte(output), errPullFailed
	}, &calls
}

func TestPullWithRetry(t *testing.T) {
	const registryBadGateway = "Error response from daemon: unexpected status from GET request " +
		"to https://registry-1.docker.io/v2/: 502 Bad Gateway"
	const tokenStatus = "failed to fetch oauth token: unexpected status from POST request " +
		"to https://auth.docker.io/token: "
	for _, tc := range []struct {
		name      string
		outputs   []string
		wantCalls int
		wantErr   bool
	}{
		{"succeeds first time", []string{""}, 1, false},
		{"retries a dropped request", []string{"failed to do request: Get https://registry.test/blob: EOF\n", ""}, 2, false},
		{"retries a token endpoint 522", []string{tokenStatus + "522", ""}, 2, false},
		{"does not retry a token endpoint 401", []string{tokenStatus + "401", ""}, 1, true},
		{"retries a registry 502", []string{registryBadGateway, registryBadGateway, ""}, 3, false},
		{"gives up after the attempt budget", []string{registryBadGateway, registryBadGateway, registryBadGateway}, 3, true},
		{"does not retry a missing manifest", []string{"Error response from daemon: manifest unknown", ""}, 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pull, calls := scriptedPull(tc.outputs...)
			err := pullWithRetry(t.Context(), pull, testImage, len(tc.outputs), 0)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, want error %v", err, tc.wantErr)
			}
			if *calls != tc.wantCalls {
				t.Fatalf("pulled %d times, want %d", *calls, tc.wantCalls)
			}
			if tc.wantErr &&
				(!errors.Is(err, errPullFailed) || !strings.Contains(err.Error(), strings.TrimSpace(tc.outputs[tc.wantCalls-1]))) {
				t.Fatalf("error = %v, want pull failure and docker output", err)
			}
		})
	}
}

func TestPullCancellationStopsRetries(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	calls := 0
	pull := func(context.Context, string) ([]byte, error) {
		calls++
		cancel()
		return []byte("503 Service Unavailable"), errPullFailed
	}
	err := pullWithRetry(ctx, pull, testImage, pullAttempts, time.Hour)
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("calls = %d, error = %v, want one call and cancellation", calls, err)
	}
}

func TestTransient(t *testing.T) {
	for _, tc := range []struct {
		output string
		want   bool
	}{
		{"failed to do request: Get https://registry.test/blob: EOF\n", true},
		{"unexpected EOF", true},
		{"manifest unknown", false},
		{"EOF", false},
		{"failed request: EOF while reading config", false},
		{"failed request: EOF permission denied", false},
	} {
		t.Run(tc.output, func(t *testing.T) {
			if got := transient([]byte(tc.output)); got != tc.want {
				t.Fatalf("transient(%q) = %v, want %v", tc.output, got, tc.want)
			}
		})
	}
}
