package main

import (
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/rogpeppe/go-internal/testscript"
	"github.com/team-loco/loco/internal/keychain"
)

const (
	scriptVersion = "test"
	coverDirEnv   = "GOCOVERDIR"
)

type fakeAPIKey struct{}

func TestMain(m *testing.M) {
	testscript.Main(m, map[string]func(){
		"loco": func() { Cli(scriptVersion) },
	})
}

func TestScripts(t *testing.T) {
	testscript.Run(t, testscript.Params{
		Dir:                 filepath.Join("testdata", "script"),
		RequireExplicitExec: true,
		Setup:               setupScript,
		Cmds: map[string]func(ts *testscript.TestScript, neg bool, args []string){
			"fakeapi": cmdFakeAPI,
		},
	})
}

func setupScript(env *testscript.Env) error {
	if err := isolateCoverage(env); err != nil {
		return err
	}
	home := filepath.Join(env.WorkDir, "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		return err
	}
	env.Setenv("HOME", home)
	env.Setenv(keychain.StoreEnvVar, "file")

	api := newFakeAPI()
	srv := httptest.NewServer(api.handler())
	env.Defer(srv.Close)
	api.platform.baseURL = srv.URL
	env.Setenv("LOCO_HOST", srv.URL)
	env.Values[fakeAPIKey{}] = api
	return fillCredentialsHost(filepath.Join(home, ".loco", "credentials.json"), srv.URL)
}

func isolateCoverage(env *testscript.Env) error {
	shared := env.Getenv(coverDirEnv)
	if shared == "" {
		return nil
	}
	dir, err := os.MkdirTemp(shared, "script-")
	if err != nil {
		return fmt.Errorf("create the script's coverage directory: %w", err)
	}
	env.Setenv(coverDirEnv, dir)
	env.Defer(func() {
		if err := collectCoverage(dir, shared); err != nil {
			env.T().Fatal(err)
		}
	})
	return nil
}

func collectCoverage(dir, shared string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read the script's coverage directory: %w", err)
	}
	for _, entry := range entries {
		from := filepath.Join(dir, entry.Name())
		to := filepath.Join(shared, entry.Name())
		if err := os.Rename(from, to); err != nil {
			return fmt.Errorf("collect coverage file %s: %w", entry.Name(), err)
		}
	}
	return nil
}

func fillCredentialsHost(path, host string) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	filled := strings.ReplaceAll(string(data), "${LOCO_HOST}", host)
	return os.WriteFile(path, []byte(filled), 0o600)
}

func cmdFakeAPI(ts *testscript.TestScript, neg bool, args []string) {
	api, ok := ts.Value(fakeAPIKey{}).(*fakeAPI)
	if !ok {
		ts.Fatalf("fakeapi: not initialized by setup")
	}
	if len(args) == 0 {
		ts.Fatalf("usage: fakeapi called <method> [authorization] | fakeapi fail <method> <code>")
	}

	switch args[0] {
	case "called":
		if len(args) < 2 || len(args) > 3 {
			ts.Fatalf("usage: fakeapi called <method> [authorization]")
		}
		authorization := ""
		if len(args) == 3 {
			authorization = args[2]
		}
		called := api.called(args[1], authorization)
		if neg && called {
			ts.Fatalf("fakeapi: %s was called unexpectedly", args[1])
		}
		if !neg && !called {
			ts.Fatalf("fakeapi: %s was not called with authorization %q", args[1], authorization)
		}
	case "fail":
		if neg || len(args) != 3 {
			ts.Fatalf("usage: fakeapi fail <method> <code>")
		}
		var code connect.Code
		if err := code.UnmarshalText([]byte(args[2])); err != nil {
			ts.Fatalf("fakeapi: %v", err)
		}
		api.fail(args[1], code)
	default:
		if !cmdFakeLogin(ts, api, neg, args) {
			cmdFakePlatform(ts, api, neg, args)
		}
	}
}
