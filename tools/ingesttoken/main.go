package main

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/team-loco/loco/internal/clusterassertion"
	"github.com/team-loco/loco/internal/ingesttoken"
	"github.com/team-loco/loco/internal/ingesttoken/ingesttest"
)

const (
	defaultLifetime    = time.Hour
	assertionLifetime  = time.Minute
	workspaceSeparator = ","
)

var (
	errMissingFlags      = errors.New("-key and -cluster are required")
	errMissingWorkspaces = errors.New("-serve requires -workspaces")
	errMissingCluster    = errors.New("-agent-key requires -cluster or -public-key")
	errPublicKey         = errors.New("agent key has no Ed25519 public key")
)

func agentAssertion(agentKey, clusterID string, now time.Time) (string, error) {
	if clusterID == "" {
		return "", errMissingCluster
	}
	key, err := ingesttest.ReadSigner(agentKey)
	if err != nil {
		return "", err
	}
	return clusterassertion.Sign(key.Key, clusterID, now, assertionLifetime)
}

func agentPublicKey(agentKey string) (string, error) {
	key, err := ingesttest.ReadSigner(agentKey)
	if err != nil {
		return "", err
	}
	public, ok := key.Key.Public().(ed25519.PublicKey)
	if !ok {
		return "", errPublicKey
	}
	return hex.EncodeToString(public), nil
}

func mint(ctx context.Context, keyFile, clusterID string, now time.Time, lifetime time.Duration) (string, error) {
	if keyFile == "" || clusterID == "" {
		return "", errMissingFlags
	}
	signer, err := ingesttest.ReadSigner(keyFile)
	if err != nil {
		return "", err
	}
	token, _, err := ingesttoken.Sign(ctx, signer, clusterID, now, lifetime)
	return token, err
}

func serve(ctx context.Context, keyFile, addr, workspaces string, placedAt time.Time) error {
	if workspaces == "" {
		return errMissingWorkspaces
	}
	signer, err := ingesttest.ReadSigner(keyFile)
	if err != nil {
		return err
	}
	var lc net.ListenConfig
	listener, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", addr, err)
	}
	controlPlane := ingesttest.Serve(listener, []ingesttest.Signer{signer})
	defer controlPlane.Close()
	for workspace := range strings.SplitSeq(workspaces, workspaceSeparator) {
		controlPlane.Place(workspace, placedAt, time.Time{})
	}
	<-ctx.Done()
	return nil
}

func main() {
	keyFile := flag.String("key", "", "file holding the 32-byte Ed25519 seed that signs tokens")
	clusterID := flag.String("cluster", "", "cluster ID for the token subject")
	lifetime := flag.Duration("lifetime", defaultLifetime, "token lifetime")
	serveUsage := "serve a fake control plane with the keys' JWKS on this address instead of printing a token"
	addr := flag.String("serve", "", serveUsage)
	workspaces := flag.String(
		"workspaces",
		"",
		"comma-separated workspace IDs the fake control plane reports as placed",
	)
	agentKey := flag.String(
		"agent-key",
		"",
		"file holding a 32-byte Ed25519 agent key seed; prints a cluster assertion for -cluster instead of a token",
	)
	printPublicKey := flag.Bool("public-key", false, "with -agent-key, print the hex public key instead")
	flag.Parse()
	if *agentKey != "" {
		var output string
		var err error
		if *printPublicKey {
			output, err = agentPublicKey(*agentKey)
		} else {
			output, err = agentAssertion(*agentKey, *clusterID, time.Now())
		}
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(output)
		return
	}
	if *addr != "" {
		ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer stop()
		placedAt := time.Unix(0, 0)
		if err := serve(ctx, *keyFile, *addr, *workspaces, placedAt); err != nil {
			log.Fatal(err)
		}
		return
	}
	token, err := mint(context.Background(), *keyFile, *clusterID, time.Now(), *lifetime)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(token)
}
