package secretkeys

import (
	"context"
	"crypto/ed25519"
	"fmt"
)

type PinnedKey struct {
	Name    string
	Version int
}

type Keys struct {
	Provider          Provider
	Ingest            *TransitSigner
	Registry          *TransitSigner
	RegistryKeyID     string
	RegistryPublicKey ed25519.PublicKey
}

// New logs in to Transit, then checks that cfg.Key is an aes256-gcm96 key created with
// derived=true, that ingestKey is an ed25519 key with a public key and, when registryKey is
// set, that it names an ed25519 key whose pinned version can sign, all within
// cfg.StartupTimeout. It returns the transit provider, which caches unwrapped data keys for
// cfg.CacheTTL, signers for the ingest and registry keys on the same mount and token, and the
// pinned registry key version's id and public key, and renews the token in the background until
// ctx is done.
func New(ctx context.Context, cfg TransitConfig, ingestKey string, registryKey *PinnedKey) (Keys, error) {
	transit, err := NewTransit(cfg)
	if err != nil {
		return Keys{}, fmt.Errorf("transit secrets key provider: %w", err)
	}
	startup, cancel := context.WithTimeout(ctx, cfg.StartupTimeout)
	defer cancel()
	lease, err := transit.logIn(startup)
	if err != nil {
		return Keys{}, fmt.Errorf("log in to transit at %s: %w", cfg.Address, err)
	}
	if _, err = transit.KeyID(startup); err != nil {
		return Keys{}, fmt.Errorf("secrets key %s/%s: %w", cfg.Mount, cfg.Key, err)
	}
	keys := Keys{Ingest: transit.Signer(ingestKey)}
	if _, err = keys.Ingest.PublicKeys(startup); err != nil {
		return Keys{}, fmt.Errorf("ingest key %s/%s: %w", cfg.Mount, ingestKey, err)
	}
	if registryKey != nil {
		keys.Registry = transit.Signer(registryKey.Name)
		keys.RegistryKeyID, keys.RegistryPublicKey, err = keys.Registry.Pin(startup, registryKey.Version)
		if err != nil {
			return Keys{}, fmt.Errorf("registry key %s/%s: %w", cfg.Mount, registryKey.Name, err)
		}
	}
	go transit.renewToken(ctx, lease)
	keys.Provider = NewDEKCache(transit, cfg.CacheTTL)
	return keys, nil
}
