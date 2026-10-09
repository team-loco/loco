package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/secretkeys"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
)

var errSecretMissing = errors.New("secret missing")

type listPlacementsFunc func(ctx context.Context, q *genDb.Queries) ([]genDb.Placement, error)

type secretSnapshot struct {
	provider     secretkeys.Provider
	environments map[uuid.UUID]genDb.Environment
	keys         map[uuid.UUID]genDb.EnvironmentKey
	ciphertexts  map[uuid.UUID][]genDb.ListSecretCiphertextsRow
	deks         map[uuid.UUID][]byte
}

func newSecretSnapshot(provider secretkeys.Provider) *secretSnapshot {
	return &secretSnapshot{
		provider:     provider,
		environments: make(map[uuid.UUID]genDb.Environment),
		keys:         make(map[uuid.UUID]genDb.EnvironmentKey),
		ciphertexts:  make(map[uuid.UUID][]genDb.ListSecretCiphertextsRow),
		deks:         make(map[uuid.UUID][]byte),
	}
}

func (s *AgentServer) readPlacements(
	ctx context.Context,
	list listPlacementsFunc,
) ([]genDb.Placement, *secretSnapshot, error) {
	snapshot := newSecretSnapshot(s.provider)
	var placements []genDb.Placement
	options := pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}
	err := pgx.BeginTxFunc(ctx, s.db, options, func(tx pgx.Tx) error {
		q := genDb.New(tx)
		listed, err := list(ctx, q)
		if err != nil {
			return err
		}
		for _, placement := range listed {
			if loadErr := snapshot.load(ctx, q, placement); loadErr != nil {
				return loadErr
			}
		}
		placements = listed
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	return placements, snapshot, nil
}

func (snap *secretSnapshot) load(ctx context.Context, q *genDb.Queries, placement genDb.Placement) error {
	if placement.DesiredDeleted || len(placement.SecretNames) == 0 {
		return nil
	}
	rows, err := q.ListSecretCiphertexts(ctx, genDb.ListSecretCiphertextsParams{
		EnvironmentID: placement.EnvironmentID,
		Names:         placement.SecretNames,
	})
	if err != nil {
		return fmt.Errorf("list secrets: %w", err)
	}
	snap.ciphertexts[placement.ID] = rows
	if _, ok := snap.environments[placement.EnvironmentID]; ok {
		return nil
	}
	env, err := q.GetEnvironmentByID(ctx, placement.EnvironmentID)
	if err != nil {
		return fmt.Errorf("get environment: %w", err)
	}
	snap.environments[env.ID] = env
	key, err := q.GetEnvironmentKey(ctx, env.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("get environment key: %w", err)
	}
	snap.keys[env.ID] = key
	return nil
}

func (snap *secretSnapshot) zero() {
	for _, dek := range snap.deks {
		secretkeys.Zero(dek)
	}
	clear(snap.deks)
}

func (snap *secretSnapshot) dek(ctx context.Context, environmentID uuid.UUID) ([]byte, error) {
	if dek, ok := snap.deks[environmentID]; ok {
		return dek, nil
	}
	if snap.provider == nil {
		return nil, ErrSecretsNotConfigured
	}
	key, ok := snap.keys[environmentID]
	if !ok {
		return nil, ErrNoEnvironmentKey
	}
	if key.FormatVersion != secretFormatVersion {
		return nil, fmt.Errorf("%w: environment key has %d", ErrUnknownSecretFormat, key.FormatVersion)
	}
	dek, err := unwrapEnvironmentKey(ctx, snap.provider, key)
	if err != nil {
		return nil, fmt.Errorf("unwrap environment key: %w", err)
	}
	snap.deks[environmentID] = dek
	return dek, nil
}

func (snap *secretSnapshot) envSecret(ctx context.Context, placement genDb.Placement) (*agentv1.EnvSecret, error) {
	rows := snap.ciphertexts[placement.ID]
	byName := make(map[string]genDb.ListSecretCiphertextsRow, len(rows))
	for _, row := range rows {
		byName[row.Name] = row
	}
	for _, name := range placement.SecretNames {
		if _, ok := byName[name]; !ok {
			return nil, fmt.Errorf("%w: %s", errSecretMissing, name)
		}
	}
	dek, err := snap.dek(ctx, placement.EnvironmentID)
	if err != nil {
		return nil, err
	}
	env := snap.environments[placement.EnvironmentID]
	data := make(map[string][]byte, len(byName))
	for name, row := range byName {
		value, openErr := openSecret(dek, env, row)
		if openErr != nil {
			zeroEnvSecretData(data)
			return nil, fmt.Errorf("decrypt %s: %w", name, openErr)
		}
		data[name] = value
	}
	return &agentv1.EnvSecret{Revision: placement.DesiredRevision, Data: data}, nil
}

func zeroEnvSecretData(data map[string][]byte) {
	for _, value := range data {
		secretkeys.Zero(value)
	}
}

func isPlacementSecretError(err error) bool {
	return errors.Is(err, errSecretMissing) || errors.Is(err, secretkeys.ErrDecrypt)
}
