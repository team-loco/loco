package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFixtureSchemaUsesHeadMigrationsBeforeBaseline(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "initialized", true: "migration failure"}[fail], func(t *testing.T) {
			bin := t.TempDir()
			commands := map[string]string{
				kubectlExecutable: `printf 'Forwarding from 127.0.0.1:15432 -> 5432\n'
exec /bin/sleep 30`,
				"goose": `[ "$*" = "-dir $SCHEMA_ROOT/api/migrations up" ] || exit 41
[ "$GOOSE_DRIVER" = postgres ] || exit 42
[ "$GOOSE_DBSTRING" = 'postgresql://fixture@127.0.0.1:15432/loco?sslmode=disable' ] || exit 43
if [ "$SCHEMA_FAIL" = yes ]; then echo 'migration rejected' >&2; exit 44; fi`,
			}
			for name, script := range commands {
				if err := os.WriteFile(
					filepath.Join(bin, name),
					[]byte("#!/bin/sh\nset -eu\n"+script+"\n"),
					0700,
				); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("PATH", bin)
			t.Setenv("SCHEMA_ROOT", bin)
			value := "no"
			if fail {
				value = "yes"
			}
			t.Setenv("SCHEMA_FAIL", value)
			f := fixture{root: bin}
			err := f.prepareSchema(
				t.Context(),
				"postgresql://fixture@postgres.namespace.svc:5432/loco?sslmode=disable",
			)
			if fail {
				if err == nil || !strings.Contains(err.Error(), "migration rejected") {
					t.Fatalf("migration failure: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
