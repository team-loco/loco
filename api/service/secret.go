package service

import (
	"bytes"
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/team-loco/loco/api/authz"
	"github.com/team-loco/loco/api/authz/actions"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/secretkeys"
	"github.com/team-loco/loco/api/timeutil"
	secretv1 "github.com/team-loco/loco/gen/go/loco/secret/v1"
	"github.com/team-loco/loco/internal/rpcerr"
)

var (
	ErrSecretValueTooLarge = errors.New("secret value exceeds the size limit")
	ErrTooManySecrets      = rpcerr.NewCoded(
		connect.CodeResourceExhausted,
		"service has reached its secret limit in the environment",
	)
	ErrSecretNotFound    = rpcerr.NewCoded(connect.CodeNotFound, "secret not found")
	ErrKeyProvider       = errors.New("secrets key provider unavailable")
	ErrSecretLockTimeout = rpcerr.NewCoded(
		connect.CodeAborted,
		"secrets of the service are locked by another operation",
	)
	ErrSecretKeyChanging     = errors.New("environment key changed during the write, retry")
	ErrPlacementSecretsLarge = rpcerr.NewCoded(
		connect.CodeFailedPrecondition,
		"secrets of a placement of the service exceed the size limit",
	)
	ErrSecretInUse = rpcerr.NewCoded(
		connect.CodeFailedPrecondition,
		"secret is declared by the service's active deployment",
	)
	ErrUnknownSecretFormat = errors.New("secret has a format version this binary does not know")

	ErrNewDEKNeedsEnvironment = errors.New("a new data key requires an environment id")
	ErrNoEnvironmentKey       = errors.New("environment has no secrets key")

	errKeyChanged = errors.New("environment key changed under the lock")
)

const secretFormatVersion int16 = 1

const setSecretsAttempts = 3

type rewrapOutcome int

const (
	rewrapCurrent rewrapOutcome = iota
	rewrapDone
	rewrapSkipped
)

// SecretConfig holds the limits SetSecrets enforces and how long a secret operation waits for
// the environment's key row lock.
type SecretConfig struct {
	MaxValueBytes     int32
	MaxPerService     int32
	MaxPlacementBytes int32
	LockTimeout       time.Duration
}

// SecretServer implements the SecretService.
type SecretServer struct {
	db       *pgxpool.Pool
	queries  genDb.Querier
	authz    *authz.Authorizer
	provider secretkeys.Provider
	config   SecretConfig
}

// NewSecretServer creates a SecretServer.
func NewSecretServer(
	db *pgxpool.Pool,
	queries genDb.Querier,
	provider secretkeys.Provider,
	config SecretConfig,
) *SecretServer {
	return &SecretServer{db: db, queries: queries, authz: authz.New(db, queries), provider: provider, config: config}
}

// secretTarget is the service and environment a secret operation addresses.
type secretTarget struct {
	env        genDb.Environment
	resourceID uuid.UUID
}

func (s *SecretServer) authorizeTarget(
	ctx context.Context,
	resourceID string,
	environmentID string,
	action actions.Action,
) (secretTarget, error) {
	parsedResourceID, err := parseID("resource_id", resourceID)
	if err != nil {
		return secretTarget{}, err
	}
	parsedEnvironmentID, err := parseID("environment_id", environmentID)
	if err != nil {
		return secretTarget{}, err
	}
	if err = authorize(ctx, s.authz, action, parsedResourceID); err != nil {
		return secretTarget{}, err
	}
	resource, err := s.queries.GetResourceByID(ctx, parsedResourceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return secretTarget{}, ErrResourceNotFound
	}
	if err != nil {
		return secretTarget{}, fmt.Errorf("get resource: %w", err)
	}
	env, err := s.queries.GetEnvironmentByID(ctx, parsedEnvironmentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return secretTarget{}, ErrEnvironmentNotFound
	}
	if err != nil {
		return secretTarget{}, fmt.Errorf("get environment: %w", err)
	}
	if env.WorkspaceID != resource.WorkspaceID {
		return secretTarget{}, ErrEnvironmentNotFound
	}
	return secretTarget{env: env, resourceID: resource.ID}, nil
}

func (t secretTarget) aad(name string, version int32) []byte {
	return secretkeys.SecretAAD(t.env.WorkspaceID.String(), t.env.ID.String(), t.resourceID.String(), name, version)
}

// SetSecrets encrypts each value under the environment's data key and stores it. A value equal
// to the stored one changes nothing.
func (s *SecretServer) SetSecrets(
	ctx context.Context,
	req *secretv1.SetSecretsRequest,
) (*secretv1.SetSecretsResponse, error) {
	target, err := s.authorizeTarget(ctx, req.GetResourceId(), req.GetEnvironmentId(), actions.SetSecrets)
	if err != nil {
		return nil, err
	}
	p, err := principalFrom(ctx)
	if err != nil {
		return nil, err
	}
	values := req.GetValues()
	names := slices.Sorted(maps.Keys(values))
	for _, name := range names {
		if int64(len(values[name])) > int64(s.config.MaxValueBytes) {
			tooLarge := fmt.Errorf("%w: %s", ErrSecretValueTooLarge, name)
			return nil, connect.NewError(connect.CodeInvalidArgument, publicInvalidRequest).WithCause(tooLarge)
		}
	}

	for range setSecretsAttempts {
		response, setErr := s.setSecretsOnce(ctx, target, p.Entity, names, values)
		if errors.Is(setErr, errKeyChanged) {
			continue
		}
		if setErr != nil {
			return nil, setErr
		}
		return response, nil
	}
	return nil, rpcerr.New(connect.CodeAborted, ErrSecretKeyChanging)
}

func (s *SecretServer) setSecretsOnce(
	ctx context.Context,
	target secretTarget,
	updatedBy genDb.Entity,
	names []string,
	values map[string]string,
) (*secretv1.SetSecretsResponse, error) {
	key, dek, err := s.environmentDEK(ctx, target.env.ID)
	if err != nil {
		return nil, err
	}
	defer secretkeys.Zero(dek)

	response := &secretv1.SetSecretsResponse{Versions: make([]*secretv1.SecretVersion, 0, len(names))}
	err = withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		locked, revision, lockErr := s.lockTarget(ctx, qtx, target)
		if lockErr != nil {
			return lockErr
		}
		if !bytes.Equal(locked.WrappedDek, key.WrappedDek) {
			return errKeyChanged
		}
		existing, listErr := qtx.ListSecretCiphertexts(ctx, genDb.ListSecretCiphertextsParams{
			ResourceID: target.resourceID, EnvironmentID: target.env.ID, Names: names,
		})
		if listErr != nil {
			return fmt.Errorf("list secret ciphertexts: %w", listErr)
		}
		current := make(map[string]genDb.ListSecretCiphertextsRow, len(existing))
		for _, row := range existing {
			current[row.Name] = row
		}

		params := genDb.UpsertSecretsParams{
			ResourceID:    target.resourceID,
			EnvironmentID: target.env.ID,
			UpdatedByType: string(updatedBy.Type),
			UpdatedByID:   updatedBy.ID,
		}
		added := int64(0)
		for _, name := range names {
			row, found := current[name]
			if found {
				same, sameErr := storedValueEquals(dek, target, row, values[name])
				if sameErr != nil {
					return sameErr
				}
				if same {
					response.Versions = append(
						response.Versions,
						&secretv1.SecretVersion{Name: name, Version: row.Version},
					)
					continue
				}
			}
			if !found {
				added++
			}
			version := row.Version + 1
			nonce, ciphertext, sealErr := secretkeys.Seal(dek, []byte(values[name]), target.aad(name, version))
			if sealErr != nil {
				return fmt.Errorf("seal %s: %w", name, sealErr)
			}
			params.Names = append(params.Names, name)
			params.Versions = append(params.Versions, version)
			params.Nonces = append(params.Nonces, nonce)
			params.Ciphertexts = append(params.Ciphertexts, ciphertext)
			params.FormatVersions = append(params.FormatVersions, secretFormatVersion)
			response.Versions = append(response.Versions, &secretv1.SecretVersion{Name: name, Version: version})
		}

		if len(params.Names) == 0 {
			response.Revision = revision
			return nil
		}
		if rolloutErr := ensureNoRollout(ctx, qtx, target.resourceID, target.env.ID, ""); rolloutErr != nil {
			return rolloutErr
		}
		return s.writeSecrets(ctx, qtx, target, revision, params, added, response)
	})
	if err != nil {
		return nil, err
	}
	return response, nil
}

func (s *SecretServer) writeSecrets(
	ctx context.Context,
	qtx *genDb.Queries,
	target secretTarget,
	lockedRevision int64,
	params genDb.UpsertSecretsParams,
	added int64,
	response *secretv1.SetSecretsResponse,
) error {
	count, countErr := qtx.CountSecrets(ctx, genDb.CountSecretsParams{
		ResourceID: target.resourceID, EnvironmentID: target.env.ID,
	})
	if countErr != nil {
		return fmt.Errorf("count secrets: %w", countErr)
	}
	if count+added > int64(s.config.MaxPerService) {
		return ErrTooManySecrets
	}
	if upsertErr := qtx.UpsertSecrets(ctx, params); upsertErr != nil {
		return fmt.Errorf("upsert secrets: %w", upsertErr)
	}
	sizes, sizesErr := qtx.ListPlacementSecretSizes(ctx, genDb.ListPlacementSecretSizesParams{
		ResourceID: target.resourceID, EnvironmentID: target.env.ID, Names: params.Names,
	})
	if sizesErr != nil {
		return fmt.Errorf("list placement secret sizes: %w", sizesErr)
	}
	for _, size := range sizes {
		total := size.NameBytes + size.CiphertextBytes - size.SecretCount*secretkeys.Overhead
		if total > int64(s.config.MaxPlacementBytes) {
			return ErrPlacementSecretsLarge.Detail("placement %s holds %d bytes", size.PlacementID, total)
		}
	}
	if rollErr := rollPlacementsForSecrets(ctx, qtx, target, params.Names); rollErr != nil {
		return rollErr
	}
	if bumpErr := bumpEnvironmentRevision(ctx, qtx, target.env.ID); bumpErr != nil {
		return bumpErr
	}
	revision := lockedRevision + 1
	response.Revision = revision
	return events.Record(ctx, qtx, target.event(events.SecretSet, params.Names, revision))
}

func (t secretTarget) event(eventType string, names []string, revision int64) events.Event {
	return events.Event{
		Type:        eventType,
		WorkspaceID: new(t.env.WorkspaceID),
		SubjectType: events.SubjectResource,
		SubjectID:   new(t.resourceID),
		Data: map[string]any{
			events.FieldNames:         names,
			events.FieldEnvironmentID: t.env.ID,
			events.FieldRevision:      revision,
		},
	}
}

func storedValueEquals(
	dek []byte,
	target secretTarget,
	row genDb.ListSecretCiphertextsRow,
	value string,
) (bool, error) {
	plaintext, err := openSecret(dek, target, row)
	if err != nil {
		return false, err
	}
	defer secretkeys.Zero(plaintext)
	return subtle.ConstantTimeCompare(plaintext, []byte(value)) == 1, nil
}

func openSecret(dek []byte, target secretTarget, row genDb.ListSecretCiphertextsRow) ([]byte, error) {
	if row.FormatVersion != secretFormatVersion {
		return nil, fmt.Errorf("%w: %s has %d", ErrUnknownSecretFormat, row.Name, row.FormatVersion)
	}
	plaintext, err := secretkeys.Open(dek, row.Nonce, row.Ciphertext, target.aad(row.Name, row.Version))
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", row.Name, err)
	}
	return plaintext, nil
}

func (s *SecretServer) lockEnvironmentKey(
	ctx context.Context,
	qtx *genDb.Queries,
	env genDb.Environment,
) (genDb.EnvironmentKey, int64, error) {
	timeoutMillis := strconv.FormatInt(s.config.LockTimeout.Milliseconds(), 10)
	if err := qtx.SetLockTimeout(ctx, timeoutMillis); err != nil {
		return genDb.EnvironmentKey{}, 0, fmt.Errorf("set lock timeout: %w", err)
	}
	revisions, err := lockWorkspaceEnvironments(ctx, qtx, env.WorkspaceID)
	if isPgLockNotAvailable(err) {
		return genDb.EnvironmentKey{}, 0, ErrSecretLockTimeout
	}
	if err != nil {
		return genDb.EnvironmentKey{}, 0, err
	}
	revision, found := revisions[env.ID]
	if !found {
		return genDb.EnvironmentKey{}, 0, ErrEnvironmentNotFound
	}
	key, err := qtx.LockEnvironmentKey(ctx, env.ID)
	if isPgLockNotAvailable(err) {
		return genDb.EnvironmentKey{}, 0, ErrSecretLockTimeout
	}
	if err != nil {
		return genDb.EnvironmentKey{}, 0, fmt.Errorf("lock environment key: %w", err)
	}
	return key, revision, nil
}

func (s *SecretServer) lockTarget(
	ctx context.Context,
	qtx *genDb.Queries,
	target secretTarget,
) (genDb.EnvironmentKey, int64, error) {
	key, revision, err := s.lockEnvironmentKey(ctx, qtx, target.env)
	if err != nil {
		return genDb.EnvironmentKey{}, 0, err
	}
	err = lockResource(ctx, qtx, target.resourceID)
	if isPgLockNotAvailable(err) {
		return genDb.EnvironmentKey{}, 0, ErrSecretLockTimeout
	}
	if err != nil {
		return genDb.EnvironmentKey{}, 0, err
	}
	return key, revision, nil
}

// DeleteSecrets removes names from a service in an environment.
func (s *SecretServer) DeleteSecrets(
	ctx context.Context,
	req *secretv1.DeleteSecretsRequest,
) (*secretv1.DeleteSecretsResponse, error) {
	target, err := s.authorizeTarget(ctx, req.GetResourceId(), req.GetEnvironmentId(), actions.DeleteSecrets)
	if err != nil {
		return nil, err
	}
	names := slices.Sorted(slices.Values(req.GetNames()))
	var revision int64
	err = withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		_, lockedRevision, lockErr := s.lockTarget(ctx, qtx, target)
		if lockErr != nil {
			if errors.Is(lockErr, pgx.ErrNoRows) {
				return ErrSecretNotFound.Detail("%s", names[0])
			}
			return lockErr
		}
		if rolloutErr := ensureNoRollout(ctx, qtx, target.resourceID, target.env.ID, ""); rolloutErr != nil {
			return rolloutErr
		}
		declared, declaredErr := qtx.ListDeclaredSecretNames(ctx, genDb.ListDeclaredSecretNamesParams{
			ResourceID: target.resourceID, EnvironmentID: target.env.ID, Names: names,
		})
		if declaredErr != nil {
			return fmt.Errorf("list declared secret names: %w", declaredErr)
		}
		if len(declared) > 0 {
			return ErrSecretInUse.Detail("%s", strings.Join(declared, ", "))
		}
		deleted, deleteErr := qtx.DeleteSecrets(ctx, genDb.DeleteSecretsParams{
			ResourceID: target.resourceID, EnvironmentID: target.env.ID, Names: names,
		})
		if deleteErr != nil {
			return fmt.Errorf("delete secrets: %w", deleteErr)
		}
		if len(deleted) != len(names) {
			slices.Sort(deleted)
			for _, name := range names {
				if _, found := slices.BinarySearch(deleted, name); !found {
					return ErrSecretNotFound.Detail("%s", name)
				}
			}
		}
		if bumpErr := bumpEnvironmentRevision(ctx, qtx, target.env.ID); bumpErr != nil {
			return bumpErr
		}
		revision = lockedRevision + 1
		return events.Record(ctx, qtx, target.event(events.SecretDeleted, names, revision))
	})
	if err != nil {
		return nil, err
	}
	return &secretv1.DeleteSecretsResponse{Revision: revision}, nil
}

// ListSecrets lists the names a service holds in an environment; it never returns a value.
func (s *SecretServer) ListSecrets(
	ctx context.Context,
	req *secretv1.ListSecretsRequest,
) (*secretv1.ListSecretsResponse, error) {
	target, err := s.authorizeTarget(ctx, req.GetResourceId(), req.GetEnvironmentId(), actions.ListSecrets)
	if err != nil {
		return nil, err
	}
	rows, err := s.queries.ListSecrets(ctx, genDb.ListSecretsParams{
		ResourceID: target.resourceID, EnvironmentID: target.env.ID,
	})
	if err != nil {
		return nil, fmt.Errorf("list secrets: %w", err)
	}
	secrets := make([]*secretv1.Secret, 0, len(rows))
	for _, row := range rows {
		secrets = append(secrets, &secretv1.Secret{
			Name:          row.Name,
			Version:       row.Version,
			UpdatedByType: row.UpdatedByType,
			UpdatedById:   row.UpdatedByID.String(),
			CreatedAt:     timeutil.ParsePostgresTimestamp(row.CreatedAt),
			UpdatedAt:     timeutil.ParsePostgresTimestamp(row.UpdatedAt),
		})
	}
	return &secretv1.ListSecretsResponse{Secrets: secrets}, nil
}

// RewrapEnvironmentKeys rewraps environment data keys under the provider's current key. An empty
// environment id selects every environment. With new_dek it replaces the environment's data key and
// re-encrypts every value under it, keeping each version.
func (s *SecretServer) RewrapEnvironmentKeys(
	ctx context.Context,
	req *secretv1.RewrapEnvironmentKeysRequest,
) (*secretv1.RewrapEnvironmentKeysResponse, error) {
	if err := authorizeSystem(ctx, s.authz, actions.RewrapEnvironmentKeys); err != nil {
		return nil, err
	}
	if req.GetNewDek() && req.GetEnvironmentId() == "" {
		return nil, rpcerr.New(connect.CodeInvalidArgument, ErrNewDEKNeedsEnvironment)
	}
	environmentIDs, err := s.rewrapTargets(ctx, req.GetEnvironmentId())
	if err != nil {
		return nil, err
	}
	response := &secretv1.RewrapEnvironmentKeysResponse{}
	for _, environmentID := range environmentIDs {
		outcome, rewrapErr := s.rewrapEnvironment(ctx, environmentID, req.GetNewDek())
		if rewrapErr != nil {
			return nil, rewrapErr
		}
		switch outcome {
		case rewrapCurrent:
		case rewrapDone:
			response.Rewrapped++
		case rewrapSkipped:
			response.Skipped++
			response.SkippedEnvironmentIds = append(response.SkippedEnvironmentIds, environmentID.String())
		}
	}
	return response, nil
}

func (s *SecretServer) rewrapTargets(ctx context.Context, environmentID string) ([]uuid.UUID, error) {
	if environmentID != "" {
		key, err := s.queries.GetEnvironmentKey(ctx, uuid.MustParse(environmentID))
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, rpcerr.New(connect.CodeNotFound, ErrNoEnvironmentKey)
		}
		if err != nil {
			return nil, fmt.Errorf("get environment key: %w", err)
		}
		return []uuid.UUID{key.EnvironmentID}, nil
	}
	keys, err := s.queries.ListEnvironmentKeys(ctx)
	if err != nil {
		return nil, fmt.Errorf("list environment keys: %w", err)
	}
	environmentIDs := make([]uuid.UUID, 0, len(keys))
	for _, key := range keys {
		environmentIDs = append(environmentIDs, key.EnvironmentID)
	}
	return environmentIDs, nil
}

func (s *SecretServer) rewrapEnvironment(
	ctx context.Context,
	environmentID uuid.UUID,
	newDEK bool,
) (rewrapOutcome, error) {
	for range setSecretsAttempts {
		key, err := s.queries.GetEnvironmentKey(ctx, environmentID)
		if err != nil {
			return rewrapSkipped, fmt.Errorf("get environment %s key: %w", environmentID, err)
		}
		outcome, err := s.rewrapEnvironmentKey(ctx, key, newDEK)
		if errors.Is(err, errKeyChanged) {
			continue
		}
		return outcome, err
	}
	return rewrapSkipped, rpcerr.New(connect.CodeAborted, ErrSecretKeyChanging)
}

func (s *SecretServer) rewrapEnvironmentKey(
	ctx context.Context,
	key genDb.EnvironmentKey,
	newDEK bool,
) (rewrapOutcome, error) {
	current, err := s.provider.KeyID(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "failed to read the current key id", "error", err)
		return rewrapSkipped, keyProviderUnavailable(err)
	}
	if key.Provider != s.provider.Name() {
		slog.WarnContext(ctx, "environment key belongs to another provider",
			"environmentId", key.EnvironmentID, "provider", key.Provider)
		return rewrapSkipped, nil
	}
	if !newDEK && key.KekID == current {
		return rewrapCurrent, nil
	}
	if key.FormatVersion != secretFormatVersion {
		return rewrapSkipped, fmt.Errorf(
			"%w: environment %s key has %d",
			ErrUnknownSecretFormat,
			key.EnvironmentID,
			key.FormatVersion,
		)
	}
	dek, err := unwrapEnvironmentKey(ctx, s.provider, key)
	if err != nil {
		slog.WarnContext(ctx, "cannot unwrap environment key",
			"environmentId", key.EnvironmentID, "kekId", key.KekID, "error", err)
		return rewrapSkipped, nil
	}
	defer secretkeys.Zero(dek)
	replacement := dek
	if newDEK {
		replacement, err = secretkeys.NewDEK()
		if err != nil {
			return rewrapSkipped, fmt.Errorf("generate a data key: %w", err)
		}
		defer secretkeys.Zero(replacement)
	}
	aad := secretkeys.DEKAAD(key.EnvironmentID.String())
	rewrapped, err := s.provider.Wrap(ctx, replacement, aad)
	if err != nil {
		slog.ErrorContext(ctx, "failed to wrap environment key", "environmentId", key.EnvironmentID, "error", err)
		return rewrapSkipped, keyProviderUnavailable(err)
	}
	env, err := s.queries.GetEnvironmentByID(ctx, key.EnvironmentID)
	if err != nil {
		return rewrapSkipped, fmt.Errorf("get environment %s: %w", key.EnvironmentID, err)
	}
	err = withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		locked, _, lockErr := s.lockEnvironmentKey(ctx, qtx, env)
		if lockErr != nil {
			return lockErr
		}
		if !bytes.Equal(locked.WrappedDek, key.WrappedDek) {
			return errKeyChanged
		}
		if newDEK {
			if reencryptErr := reencryptSecrets(ctx, qtx, env, dek, replacement); reencryptErr != nil {
				return reencryptErr
			}
		}
		if _, updateErr := qtx.RewrapEnvironmentKey(ctx, genDb.RewrapEnvironmentKeyParams{
			EnvironmentID:      env.ID,
			Provider:           rewrapped.Provider,
			KekID:              rewrapped.KeyID,
			WrappedDek:         rewrapped.Bytes,
			PreviousWrappedDek: key.WrappedDek,
		}); updateErr != nil {
			return fmt.Errorf("rewrap environment key: %w", updateErr)
		}
		return events.Record(ctx, qtx, events.Event{
			Type:        events.SecretKeyRewrapped,
			WorkspaceID: new(env.WorkspaceID),
			SubjectType: events.SubjectEnvironment,
			SubjectID:   new(env.ID),
			Data: map[string]any{
				events.FieldEnvironmentID: env.ID,
				events.FieldKekID:         rewrapped.KeyID,
				events.FieldNewDEK:        newDEK,
			},
		})
	})
	if err != nil {
		return rewrapSkipped, err
	}
	return rewrapDone, nil
}

func reencryptSecrets(
	ctx context.Context,
	qtx *genDb.Queries,
	env genDb.Environment,
	oldDEK []byte,
	newDEK []byte,
) error {
	rows, err := qtx.ListEnvironmentSecretCiphertexts(ctx, env.ID)
	if err != nil {
		return fmt.Errorf("list environment secret ciphertexts: %w", err)
	}
	params := genDb.ReencryptSecretsParams{EnvironmentID: env.ID}
	for _, row := range rows {
		target := secretTarget{env: env, resourceID: row.ResourceID}
		stored := genDb.ListSecretCiphertextsRow{
			Name:          row.Name,
			Version:       row.Version,
			Nonce:         row.Nonce,
			Ciphertext:    row.Ciphertext,
			FormatVersion: row.FormatVersion,
		}
		plaintext, openErr := openSecret(oldDEK, target, stored)
		if openErr != nil {
			return openErr
		}
		nonce, ciphertext, sealErr := secretkeys.Seal(newDEK, plaintext, target.aad(row.Name, row.Version))
		secretkeys.Zero(plaintext)
		if sealErr != nil {
			return fmt.Errorf("seal %s: %w", row.Name, sealErr)
		}
		params.ResourceIds = append(params.ResourceIds, row.ResourceID)
		params.Names = append(params.Names, row.Name)
		params.Nonces = append(params.Nonces, nonce)
		params.Ciphertexts = append(params.Ciphertexts, ciphertext)
	}
	updated, err := qtx.ReencryptSecrets(ctx, params)
	if err != nil {
		return fmt.Errorf("re-encrypt secrets: %w", err)
	}
	if updated != int64(len(rows)) {
		return fmt.Errorf("re-encrypted %d of %d secrets of environment %s", updated, len(rows), env.ID)
	}
	return nil
}

func (s *SecretServer) environmentDEK(
	ctx context.Context,
	environmentID uuid.UUID,
) (genDb.EnvironmentKey, []byte, error) {
	aad := secretkeys.DEKAAD(environmentID.String())
	key, err := s.queries.GetEnvironmentKey(ctx, environmentID)
	if errors.Is(err, pgx.ErrNoRows) {
		key, err = s.createEnvironmentKey(ctx, environmentID, aad)
	}
	if err != nil {
		return genDb.EnvironmentKey{}, nil, err
	}
	if key.FormatVersion != secretFormatVersion {
		return genDb.EnvironmentKey{}, nil, fmt.Errorf(
			"%w: environment %s key has %d",
			ErrUnknownSecretFormat,
			environmentID,
			key.FormatVersion,
		)
	}
	dek, err := unwrapEnvironmentKey(ctx, s.provider, key)
	if err != nil {
		slog.ErrorContext(ctx, "failed to unwrap environment key", "environmentId", environmentID, "error", err)
		return genDb.EnvironmentKey{}, nil, keyProviderUnavailable(err)
	}
	return key, dek, nil
}

func unwrapEnvironmentKey(ctx context.Context, provider secretkeys.Provider, key genDb.EnvironmentKey) ([]byte, error) {
	aad := secretkeys.DEKAAD(key.EnvironmentID.String())
	wrapped := secretkeys.WrappedKey{Provider: key.Provider, KeyID: key.KekID, Bytes: key.WrappedDek}
	return provider.Unwrap(ctx, wrapped, aad)
}

func (s *SecretServer) createEnvironmentKey(
	ctx context.Context,
	environmentID uuid.UUID,
	aad []byte,
) (genDb.EnvironmentKey, error) {
	dek, err := secretkeys.NewDEK()
	if err != nil {
		return genDb.EnvironmentKey{}, fmt.Errorf("generate a data key: %w", err)
	}
	defer secretkeys.Zero(dek)
	wrapped, err := s.provider.Wrap(ctx, dek, aad)
	if err != nil {
		slog.ErrorContext(ctx, "failed to wrap a data key", "environmentId", environmentID, "error", err)
		return genDb.EnvironmentKey{}, keyProviderUnavailable(err)
	}
	key, err := s.queries.InsertEnvironmentKey(ctx, genDb.InsertEnvironmentKeyParams{
		EnvironmentID: environmentID,
		Provider:      wrapped.Provider,
		KekID:         wrapped.KeyID,
		WrappedDek:    wrapped.Bytes,
		FormatVersion: secretFormatVersion,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		key, err = s.queries.GetEnvironmentKey(ctx, environmentID)
	}
	if err != nil {
		return genDb.EnvironmentKey{}, fmt.Errorf("store environment %s key: %w", environmentID, err)
	}
	return key, nil
}

// keyProviderUnavailable reports a key provider failure the client can retry; err stays private.
func keyProviderUnavailable(err error) *connect.Error {
	return connect.NewError(connect.CodeUnavailable, ErrKeyProvider.Error()).WithCause(err)
}
