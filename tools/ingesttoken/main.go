package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/team-loco/loco/internal/ingesttoken"
	"github.com/team-loco/loco/internal/ingesttoken/ingesttest"
)

const (
	defaultLifetime    = time.Hour
	workspaceSeparator = ","
)

var (
	errMissingFlags      = errors.New("-keys and -cluster are required")
	errMissingWorkspaces = errors.New("-serve requires -workspaces")
)

func signingKeys(raw string) ([]ingesttoken.SigningKey, error) {
	keys, err := ingesttoken.ParseSigningKeys(raw)
	if err != nil {
		return nil, fmt.Errorf("parse -keys: %w", err)
	}
	return keys, nil
}

func mint(rawKeys, clusterID string, now time.Time, lifetime time.Duration) (string, error) {
	if rawKeys == "" || clusterID == "" {
		return "", errMissingFlags
	}
	keys, err := signingKeys(rawKeys)
	if err != nil {
		return "", err
	}
	token, _, err := ingesttoken.Sign(keys[0], clusterID, now, lifetime)
	return token, err
}

func serve(ctx context.Context, rawKeys, addr, workspaces string, placedAt time.Time) error {
	if workspaces == "" {
		return errMissingWorkspaces
	}
	keys, err := signingKeys(rawKeys)
	if err != nil {
		return err
	}
	var lc net.ListenConfig
	listener, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", addr, err)
	}
	controlPlane := ingesttest.Serve(listener, keys)
	defer controlPlane.Close()
	for workspace := range strings.SplitSeq(workspaces, workspaceSeparator) {
		controlPlane.Place(workspace, placedAt, time.Time{})
	}
	<-ctx.Done()
	return nil
}

func main() {
	keys := flag.String("keys", "", "signing keys in LOCO_INGEST_SIGNING_KEYS format; the first signs")
	clusterID := flag.String("cluster", "", "cluster ID for the token subject")
	lifetime := flag.Duration("lifetime", defaultLifetime, "token lifetime")
	serveUsage := "serve a fake control plane with the keys' JWKS on this address instead of printing a token"
	addr := flag.String("serve", "", serveUsage)
	workspaces := flag.String(
		"workspaces",
		"",
		"comma-separated workspace IDs the fake control plane reports as placed",
	)
	flag.Parse()
	if *addr != "" {
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		placedAt := time.Unix(0, 0)
		if err := serve(ctx, *keys, *addr, *workspaces, placedAt); err != nil {
			log.Fatal(err)
		}
		return
	}
	token, err := mint(*keys, *clusterID, time.Now(), *lifetime)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(token)
}
