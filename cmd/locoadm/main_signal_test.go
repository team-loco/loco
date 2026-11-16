//go:build unix

package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"slices"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/team-loco/loco/cmd/internal/ui"

	"github.com/team-loco/loco/cmd/internal/keychain"
)

const (
	signalHelperEnv   = "LOCO_TEST_SIGNAL_HELPER"
	signalTestTimeout = 10 * time.Second
)

func TestSignalsCancelRunningCommand(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, signal := range []os.Signal{os.Interrupt, syscall.SIGTERM} {
		t.Run(signal.String(), func(t *testing.T) {
			received := make(chan struct{})
			notify := sync.OnceFunc(func() { close(received) })
			server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				notify()
				<-r.Context().Done()
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(t.Context(), signalTestTimeout)
			defer cancel()
			child := exec.CommandContext(ctx, executable, "-test.run=^TestLocoadmSignalChild$", "--",
				"login", "--api", server.URL, "--web", server.URL, "--device")
			child.Env = append(os.Environ(), signalHelperEnv+"=1",
				"HOME="+t.TempDir(), keychain.StoreEnvVar+"=file")
			if err := child.Start(); err != nil {
				t.Fatal(err)
			}
			select {
			case <-received:
			case <-ctx.Done():
				waitErr := child.Wait()
				t.Fatalf("command did not reach the API: %v", waitErr)
			}
			if err := child.Process.Signal(signal); err != nil {
				cancel()
				waitErr := child.Wait()
				t.Fatalf("signal command: %v; wait: %v", err, waitErr)
			}
			err := child.Wait()
			exit, ok := errors.AsType[*exec.ExitError](err)
			if !ok || exit.ExitCode() != ui.ExitFailure {
				t.Fatalf("signal terminated the process instead of canceling the command: %v", err)
			}
		})
	}
}

func TestLocoadmSignalChild(t *testing.T) {
	if os.Getenv(signalHelperEnv) != "1" {
		return
	}
	separator := slices.Index(os.Args, "--")
	if separator < 0 {
		t.Fatal("missing command arguments")
	}
	os.Args = append(os.Args[:1], os.Args[separator+1:]...)
	main()
}
