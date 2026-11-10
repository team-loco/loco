package main

import (
	"context"
	"crypto/ed25519"
	"fmt"

	"github.com/team-loco/loco/api/pkg/secretkeys"
	"github.com/team-loco/loco/api/pkg/transit"
)

type transitKeys struct {
	Provider          *secretkeys.DEKCache
	Ingest            *transit.Signer
	Registry          *transit.Signer
	RegistryKeyID     string
	RegistryPublicKey ed25519.PublicKey
}

// newTransitKeys logs in to Transit with one client and, all within one cfg.StartupTimeout,
// checks that the secrets key is an aes256-gcm96 key created with derived=true, that the
// ingest key is an ed25519 key with a public key and, when registryKey is set, that it names
// an ed25519 key whose pinned version can sign. It returns the secrets key provider, signers
// for the ingest and registry keys, and the pinned registry key version's id and public key.
// Once every check passes, the client renews its token until ctx is done.
func newTransitKeys(
	ctx context.Context,
	cfg transit.Config,
	secrets secretkeys.Config,
	ingestKey string,
	registryKey *transit.PinnedKey,
) (transitKeys, error) {
	startup, cancel := context.WithTimeout(ctx, cfg.StartupTimeout)
	defer cancel()
	client, err := transit.New(startup, cfg)
	if err != nil {
		return transitKeys{}, err
	}
	provider, err := secretkeys.New(startup, client, secrets)
	if err != nil {
		return transitKeys{}, fmt.Errorf("secrets key %s/%s: %w", cfg.Mount, secrets.Key, err)
	}
	keys := transitKeys{Provider: provider, Ingest: client.Signer(ingestKey)}
	if _, err = keys.Ingest.PublicKeys(startup); err != nil {
		return transitKeys{}, fmt.Errorf("ingest key %s/%s: %w", cfg.Mount, ingestKey, err)
	}
	if registryKey != nil {
		keys.Registry = client.Signer(registryKey.Name)
		keys.RegistryKeyID, keys.RegistryPublicKey, err = keys.Registry.Pin(startup, registryKey.Version)
		if err != nil {
			return transitKeys{}, fmt.Errorf("registry key %s/%s: %w", cfg.Mount, registryKey.Name, err)
		}
	}
	go client.Renew(ctx)
	return keys, nil
}
