package main

import (
	"bufio"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestServiceListenerProcess(t *testing.T) {
	if os.Getenv("LOCALSTACK_TEST_LISTENER") != "1" {
		return
	}
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	fmt.Println(listener.Addr().String())
	server := &http.Server{
		ReadHeaderTimeout: time.Second,
		Handler: http.HandlerFunc(
			func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) },
		),
	}
	err = server.Serve(listener)
	if err != nil {
		t.Fatal(err)
	}
}

func TestInstalledStackCleanupPreservesMappedAPIListener(t *testing.T) {
	cfg := testConfig(t)
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := exec.CommandContext(t.Context(), binary, "-test.run=^TestServiceListenerProcess$")
	child.Env = append(os.Environ(), "LOCALSTACK_TEST_LISTENER=1")
	output, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if killErr := child.Process.Kill(); killErr != nil &&
			!errors.Is(killErr, os.ErrProcessDone) {
			t.Error(killErr)
		}
		if waitErr := child.Wait(); waitErr != nil {
			if _, ok := errors.AsType[*exec.ExitError](waitErr); !ok {
				t.Error(waitErr)
			}
		}
	})
	scanner := bufio.NewScanner(output)
	if !scanner.Scan() {
		t.Fatalf("listener address: %v", scanner.Err())
	}
	address := scanner.Text()
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatal(err)
	}
	cleanup := exec.CommandContext(t.Context(), "bash", "mise-tasks/services/stop")
	cleanup.Env = append(os.Environ(), "STACK_STATE="+cfg.State, "KIND_CLUSTER=localstack-cleanup-test",
		"APP_PORT=:"+port, "OBS_PROXY_PORT="+port, "OBS_OTLP_GRPC_PORT="+port, "OBS_OTLP_HTTP_PORT="+port, "WEB_PORT=0")
	if result, runErr := cleanup.CombinedOutput(); runErr != nil {
		t.Fatalf("cleanup: %s: %v", result, runErr)
	}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+address, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("cleanup terminated mapped API listener: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("listener status: %d", response.StatusCode)
	}
}
