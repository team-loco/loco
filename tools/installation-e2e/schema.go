package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Unreleased migrations are edited in place. Start with the head schema, then
// temporarily restore the columns the published API still reads and writes.
func (f *fixture) prepareSchema(ctx context.Context, databaseURL string) (result error) {
	ctx, cancel := context.WithTimeout(ctx, gatewayReadyTimeout)
	defer cancel()
	database, err := url.Parse(databaseURL)
	if err != nil {
		return err
	}
	forward := exec.CommandContext(ctx, "kubectl", "-n", fixtureNamespace,
		"port-forward", "service/postgres", ":"+database.Port(), "--address=127.0.0.1")
	stdout, err := forward.StdoutPipe()
	if err != nil {
		return err
	}
	var diagnostic boundedOutput
	forward.Stderr = &diagnostic
	if err = forward.Start(); err != nil {
		return err
	}
	defer func() {
		result = errors.Join(result, stopProcess(forward))
		if result != nil && len(diagnostic.Bytes()) > 0 {
			result = fmt.Errorf("%w; database port-forward stderr: %s", result, diagnostic.Bytes())
		}
	}()
	scanner := bufio.NewScanner(stdout)
	if !scanner.Scan() {
		return errors.New("database port-forward did not start")
	}
	address, ok := strings.CutPrefix(scanner.Text(), "Forwarding from ")
	address, _, found := strings.Cut(address, " -> ")
	if !ok || !found {
		return errors.New("unexpected database port-forward response")
	}
	database.Host = address
	migrate := exec.CommandContext(ctx, "goose", "-dir", filepath.Join(f.root, "api", "migrations"), "up")
	migrate.Env = append(os.Environ(), "GOOSE_DRIVER=postgres", "GOOSE_DBSTRING="+database.String())
	if output, migrateErr := migrate.CombinedOutput(); migrateErr != nil {
		return fmt.Errorf("initialize fixture database schema: %w; output: %s", migrateErr, output)
	}
	return fixtureSchemaQuery(
		ctx,
		`ALTER TABLE environments ADD COLUMN environment_type TEXT NOT NULL DEFAULT 'production';
ALTER TABLE clusters ADD COLUMN tier TEXT NOT NULL DEFAULT 'production';`,
	)
}

func fixtureSchemaQuery(ctx context.Context, statement string) error {
	_, err := kubectl(
		ctx,
		"-n",
		fixtureNamespace,
		"exec",
		"deployment/postgres",
		"--",
		"sh",
		"-c",
		`exec psql --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" --no-psqlrc --set ON_ERROR_STOP=on --command "$1"`,
		"--",
		statement,
	)
	return err
}
