package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	"github.com/team-loco/loco/cmd/internal/keychain"
	configv1 "github.com/team-loco/loco/gen/go/loco/config/v1"
	"github.com/team-loco/loco/gen/go/loco/config/v1/configv1connect"
)

func runLocoadm(t *testing.T, args ...string) error {
	t.Helper()
	root := NewRootCmd()
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.SetArgs(args)
	return root.ExecuteContext(t.Context())
}

type configWithoutAuth struct {
	configv1connect.UnimplementedConfigServiceHandler
	called chan struct{}
}

func (c *configWithoutAuth) GetConfig(
	context.Context, *configv1.GetConfigRequest,
) (*configv1.GetConfigResponse, error) {
	close(c.called)
	return &configv1.GetConfigResponse{}, nil
}

func TestLoginSignsInToTheFlaggedAPI(t *testing.T) {
	config := &configWithoutAuth{called: make(chan struct{})}
	server := connect.NewServer()
	configv1connect.RegisterConfigServiceHandler(server, config)
	mux := http.NewServeMux()
	connecthttp.Mount(mux, server)
	api := httptest.NewServer(mux)
	t.Cleanup(api.Close)
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("HOME", t.TempDir())
	t.Setenv(keychain.StoreEnvVar, "file")

	err := runLocoadm(t, "login", "--api", api.URL, "--web", api.URL, "--device")
	if err == nil || !strings.Contains(err.Error(), "login to "+api.URL+" failed") {
		t.Fatalf("login = %v, want a sign-in attempt against %s", err, api.URL)
	}
	select {
	case <-config.called:
	case <-time.After(time.Second):
		t.Fatal("login did not ask the selected API for its configuration")
	}
}

func TestLoginNamesTheURLForABareHost(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv(keychain.StoreEnvVar, "file")
	tests := []struct{ api, web, want string }{
		{"api.loco.test", "https://loco.test", "set --api to a URL such as https://api.loco.test"},
		{"https://api.loco.test", "loco.test", "set --web to a URL such as https://loco.test"},
		{"localhost:8080", "https://loco.test", "set --api to a URL such as https://localhost:8080"},
	}
	for _, test := range tests {
		err := runLocoadm(t, "login", "--api", test.api, "--web", test.web, "--device")
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Errorf("login --api %s --web %s = %v, want %q", test.api, test.web, err, test.want)
		}
	}
}

func TestUpdateCommand(t *testing.T) {
	root := NewRootCmd()
	cmd, _, err := root.Find([]string{"update"})
	if err != nil || cmd.Name() != "update" || cmd.RunE == nil {
		t.Fatalf("update command not registered: %v", err)
	}
	if err := runLocoadm(t, "update", "unexpected"); err == nil {
		t.Fatal("update accepted an argument")
	}
}
