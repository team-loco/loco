package loco

import (
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/rogpeppe/go-internal/testscript"
	"github.com/team-loco/loco/internal/keychain"
)

type fakeAPIKey struct{}

func TestMain(m *testing.M) {
	testscript.Main(m, map[string]func(){
		"loco": Cli,
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
		cmdFakePlatform(ts, api, neg, args)
	}
}
