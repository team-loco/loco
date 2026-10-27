package main

import (
	"context"
	"errors"
	"testing"
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
	for _, tc := range []struct {
		name      string
		outputs   []string
		wantCalls int
		wantErr   bool
	}{
		{"succeeds first time", []string{""}, 1, false},
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
		})
	}
}

func TestRunRequiresAnImage(t *testing.T) {
	if err := run(t.Context(), nil); !errors.Is(err, errUsage) {
		t.Fatalf("err = %v, want %v", err, errUsage)
	}
}
