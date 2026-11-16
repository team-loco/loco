package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	"github.com/team-loco/loco/cmd/internal/installation"
	"github.com/team-loco/loco/cmd/internal/keychain"
	configv1 "github.com/team-loco/loco/gen/go/loco/config/v1"
	"github.com/team-loco/loco/gen/go/loco/config/v1/configv1connect"
)

// writeInstallation writes the example installation file to dir with its API URL set to apiURL.
const clusterCommand = "cluster"

func writeInstallation(t *testing.T, dir, apiURL string) {
	t.Helper()
	data, err := os.ReadFile("../internal/installation/testdata/" + installation.DefaultFile)
	if err != nil {
		t.Fatal(err)
	}
	data = bytes.Replace(data, []byte("https://api.example.com"), []byte(apiURL), 1)
	if err = os.WriteFile(filepath.Join(dir, installation.DefaultFile), data, 0600); err != nil {
		t.Fatal(err)
	}
}

func runLocoadm(t *testing.T, args ...string) error {
	t.Helper()
	root := NewRootCmd()
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.SetArgs(args)
	return root.ExecuteContext(t.Context())
}

func TestInstallationCommandsReadTheDefaultFile(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("HOME", t.TempDir())
	t.Setenv(keychain.StoreEnvVar, "file")
	if err := os.WriteFile(installation.DefaultFile, []byte("version: 99\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"validate"}, {"plan"}, {"apply"}, {clusterCommand, "attach"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			err := runLocoadm(t, args...)
			// The validation error of ./loco-install.yaml shows the command read it without --file.
			if err == nil || err.Error() != "name: required field is missing" {
				t.Fatalf("%v = %v, want the validation error of ./%s", args, err, installation.DefaultFile)
			}
		})
	}
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
	writeInstallation(t, dir, "https://unused.example.invalid")
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

func TestClusterAttachRequiresASession(t *testing.T) {
	dir := t.TempDir()
	writeInstallation(t, dir, "https://api.example.com")
	t.Chdir(dir)
	t.Setenv("HOME", t.TempDir())
	t.Setenv(keychain.StoreEnvVar, "file")
	err := runLocoadm(t, clusterCommand, "attach")
	if err == nil || !strings.Contains(err.Error(), "locoadm login") {
		t.Fatalf("attach without a session = %v, want a pointer to locoadm login", err)
	}
}
