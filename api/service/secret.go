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
	"github.com/team-loco/loco/api/contextkeys"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/secretkeys"
	"github.com/team-loco/loco/api/timeutil"
	secretv1 "github.com/team-loco/loco/gen/go/loco/secret/v1"
)

var (
	ErrSecretsNotConfigured  = errors.New("no secrets key provider is configured")
	ErrSecretValueTooLarge   = errors.New("secret value exceeds the size limit")
	ErrTooManySecrets        = errors.New("service has reached its secret limit in the environment")
	ErrSecretNotFound        = errors.New("secret not found")
	ErrKeyProvider           = errors.New("secrets key provider unavailable")
	ErrSecretLockTimeout     = errors.New("secrets of the service are locked by another operation")
	ErrSecretKeyChanging     = errors.New("environment key changed during the write, retry")
	ErrPlacementSecretsLarge = errors.New("secrets of a placement of the service exceed the size limit")
	ErrSecretInUse           = errors.New("secret is declared by the service's active deployment")
	ErrUnknownSecretFormat   = errors.New("secret has a format version this binary does not know")

	ErrNewDEKNeedsEnvironment = errors.New("a new data key requires an environment id")
	ErrNoEnvironmentKey       = errors.New("environment has no secrets key")

	errKeyChanged = errors.New("environment key changed under the lock")
)

const secretFormatVersion int16 = 1

const (
	secretTagBytes     = 16
	setSecretsAttempts = 3
)

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

// NewSecretServer creates a SecretServer; a nil provider refuses every write with FailedPrecondition.
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
	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return secretTarget{}, connect.NewError(connect.CodeInternal, errEntityScopesNotFound.Error()).
			WithCause(errEntityScopesNotFound)
	}
	required := actions.New(action, resourceID)
	if err := s.authz.Check(ctx, scopes, required); err != nil {
		slog.WarnContext(ctx, "unauthorized secret access", "resourceId", resourceID)
		return secretTarget{}, connect.NewError(connect.CodePermissionDenied, err.Error()).WithCause(err)
	}
	resource, err := s.queries.GetResourceByID(ctx, uuid.MustParse(resourceID))
	if errors.Is(err, pgx.ErrNoRows) {
		return secretTarget{}, connect.NewError(connect.CodeNotFound, ErrResourceNotFound.Error()).
			WithCause(ErrResourceNotFound)
	}
	if err != nil {
		slog.ErrorContext(ctx, "failed to load resource", "error", err)
		return secretTarget{}, connect.NewError(connect.CodeInternal, ErrDB.Error()).WithCause(ErrDB)
	}
	env, err := s.queries.GetEnvironmentByID(ctx, uuid.MustParse(environmentID))
	if errors.Is(err, pgx.ErrNoRows) {
		return secretTarget{}, connect.NewError(connect.CodeNotFound, ErrEnvironmentNotFound.Error()).
			WithCause(ErrEnvironmentNotFound)
	}
	if err != nil {
		slog.ErrorContext(ctx, "failed to load environment", "error", err)
		return secretTarget{}, connect.NewError(connect.CodeInternal, ErrDB.Error()).WithCause(ErrDB)
	}
	if env.WorkspaceID != resource.WorkspaceID {
		return secretTarget{}, connect.NewError(connect.CodeNotFound, ErrEnvironmentNotFound.Error()).
			WithCause(ErrEnvironmentNotFound)
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
	r := req
	target, err := s.authorizeTarget(ctx, r.GetResourceId(), r.GetEnvironmentId(), actions.SetSecrets)
	if err != nil {
		return nil, err
	}
	entity, ok := ctx.Value(contextkeys.EntityKey).(genDb.Entity)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, ErrUnauthorized.Error()).WithCause(ErrUnauthorized)
	}
	if s.provider == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrSecretsNotConfigured.Error()).
			WithCause(ErrSecretsNotConfigured)
	}
	values := r.GetValues()
	names := slices.Sorted(maps.Keys(values))
	for _, name := range names {
		if int64(len(values[name])) > int64(s.config.MaxValueBytes) {
			tooLarge := fmt.Errorf("%w: %s", ErrSecretValueTooLarge, name)
			return nil, connect.NewError(connect.CodeInvalidArgument, tooLarge.Error()).WithCause(tooLarge)
		}
	}

	for range setSecretsAttempts {
		response, setErr := s.setSecretsOnce(ctx, target, entity, names, values)
		if errors.Is(setErr, errKeyChanged) {
			continue
		}
		if setErr != nil {
			return nil, setErr
		}
		return response, nil
	}
	return nil, connect.NewError(connect.CodeAborted, ErrSecretKeyChanging.Error()).WithCause(ErrSecretKeyChanging)
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
			return listErr
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
		return s.writeSecrets(ctx, qtx, target, revision, params, added, response)
	})
	if err != nil {
		if errors.Is(err, errKeyChanged) {
			return nil, err
		}
		return nil, txError(ctx, "failed to set secrets", err)
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
		return countErr
	}
	if count+added > int64(s.config.MaxPerService) {
		return connect.NewError(connect.CodeResourceExhausted, ErrTooManySecrets.Error()).WithCause(ErrTooManySecrets)
	}
	if upsertErr := qtx.UpsertSecrets(ctx, params); upsertErr != nil {
		return upsertErr
	}
	sizes, sizesErr := qtx.ListPlacementSecretSizes(ctx, genDb.ListPlacementSecretSizesParams{
		ResourceID: target.resourceID, EnvironmentID: target.env.ID, Names: params.Names,
	})
	if sizesErr != nil {
		return sizesErr
	}
	for _, size := range sizes {
		total := size.NameBytes + size.CiphertextBytes - size.SecretCount*secretTagBytes
		if total > int64(s.config.MaxPlacementBytes) {
			tooLarge := fmt.Errorf("%w: placement %s holds %d bytes", ErrPlacementSecretsLarge, size.PlacementID, total)
			return connect.NewError(connect.CodeFailedPrecondition, tooLarge.Error()).WithCause(tooLarge)
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
		return genDb.EnvironmentKey{}, 0, err
	}
	revisions, err := lockWorkspaceEnvironments(ctx, qtx, env.WorkspaceID)
	if isPgLockNotAvailable(err) {
		return genDb.EnvironmentKey{}, 0, connect.NewError(connect.CodeAborted, ErrSecretLockTimeout.Error()).
			WithCause(ErrSecretLockTimeout)
	}
	if err != nil {
		return genDb.EnvironmentKey{}, 0, err
	}
	revision, found := revisions[env.ID]
	if !found {
		return genDb.EnvironmentKey{}, 0, connect.NewError(connect.CodeNotFound, ErrEnvironmentNotFound.Error()).
			WithCause(ErrEnvironmentNotFound)
	}
	key, err := qtx.LockEnvironmentKey(ctx, env.ID)
	if isPgLockNotAvailable(err) {
		return genDb.EnvironmentKey{}, 0, connect.NewError(connect.CodeAborted, ErrSecretLockTimeout.Error()).
			WithCause(ErrSecretLockTimeout)
	}
	return key, revision, err
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
		return genDb.EnvironmentKey{}, 0, connect.NewError(connect.CodeAborted, ErrSecretLockTimeout.Error()).
			WithCause(ErrSecretLockTimeout)
	}
	if errors.Is(err, ErrResourceNotFound) {
		return genDb.EnvironmentKey{}, 0, connect.NewError(connect.CodeNotFound, ErrResourceNotFound.Error()).
			WithCause(ErrResourceNotFound)
	}
	return key, revision, err
}

// DeleteSecrets removes names from a service in an environment.
func (s *SecretServer) DeleteSecrets(
	ctx context.Context,
	req *secretv1.DeleteSecretsRequest,
) (*secretv1.DeleteSecretsResponse, error) {
	r := req
	target, err := s.authorizeTarget(ctx, r.GetResourceId(), r.GetEnvironmentId(), actions.DeleteSecrets)
	if err != nil {
		return nil, err
	}
	names := slices.Sorted(slices.Values(r.GetNames()))
	var revision int64
	err = withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		_, lockedRevision, lockErr := s.lockTarget(ctx, qtx, target)
		if lockErr != nil {
			if errors.Is(lockErr, pgx.ErrNoRows) {
				return connect.NewError(connect.CodeNotFound, fmt.Errorf("%w: %s", ErrSecretNotFound, names[0]).Error())
			}
			return lockErr
		}
		declared, declaredErr := qtx.ListDeclaredSecretNames(ctx, genDb.ListDeclaredSecretNamesParams{
			ResourceID: target.resourceID, EnvironmentID: target.env.ID, Names: names,
		})
		if declaredErr != nil {
			return declaredErr
		}
		if len(declared) > 0 {
			inUse := fmt.Errorf("%w: %s", ErrSecretInUse, strings.Join(declared, ", "))
			return connect.NewError(connect.CodeFailedPrecondition, inUse.Error()).WithCause(inUse)
		}
		deleted, deleteErr := qtx.DeleteSecrets(ctx, genDb.DeleteSecretsParams{
			ResourceID: target.resourceID, EnvironmentID: target.env.ID, Names: names,
		})
		if deleteErr != nil {
			return deleteErr
		}
		if len(deleted) != len(names) {
			slices.Sort(deleted)
			for _, name := range names {
				if _, found := slices.BinarySearch(deleted, name); !found {
					return connect.NewError(connect.CodeNotFound, fmt.Errorf("%w: %s", ErrSecretNotFound, name).Error())
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
		return nil, txError(ctx, "failed to delete secrets", err)
	}
	return &secretv1.DeleteSecretsResponse{Revision: revision}, nil
}

// ListSecrets lists the names a service holds in an environment; it never returns a value.
func (s *SecretServer) ListSecrets(
	ctx context.Context,
	req *secretv1.ListSecretsRequest,
) (*secretv1.ListSecretsResponse, error) {
	r := req
	target, err := s.authorizeTarget(ctx, r.GetResourceId(), r.GetEnvironmentId(), actions.ListSecrets)
	if err != nil {
		return nil, err
	}
	rows, err := s.queries.ListSecrets(ctx, genDb.ListSecretsParams{
		ResourceID: target.resourceID, EnvironmentID: target.env.ID,
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to list secrets", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB.Error()).WithCause(ErrDB)
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
	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return nil, connect.NewError(connect.CodeInternal, errEntityScopesNotFound.Error()).
			WithCause(errEntityScopesNotFound)
	}
	if err := s.authz.Check(ctx, scopes, actions.NewSystem(actions.RewrapEnvironmentKeys)); err != nil {
		slog.WarnContext(ctx, "unauthorized to rewrap environment keys")
		return nil, connect.NewError(connect.CodePermissionDenied, err.Error()).WithCause(err)
	}
	if s.provider == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrSecretsNotConfigured.Error()).
			WithCause(ErrSecretsNotConfigured)
	}
	r := req
	if r.GetNewDek() && r.GetEnvironmentId() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, ErrNewDEKNeedsEnvironment.Error()).
			WithCause(ErrNewDEKNeedsEnvironment)
	}
	environmentIDs, err := s.rewrapTargets(ctx, r.GetEnvironmentId())
	if err != nil {
		return nil, err
	}
	response := &secretv1.RewrapEnvironmentKeysResponse{}
	for _, environmentID := range environmentIDs {
		outcome, rewrapErr := s.rewrapEnvironment(ctx, environmentID, r.GetNewDek())
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
			return nil, connect.NewError(connect.CodeNotFound, ErrNoEnvironmentKey.Error()).
				WithCause(ErrNoEnvironmentKey)
		}
		if err != nil {
			slog.ErrorContext(ctx, "failed to load the environment key", "error", err)
			return nil, connect.NewError(connect.CodeInternal, ErrDB.Error()).WithCause(ErrDB)
		}
		return []uuid.UUID{key.EnvironmentID}, nil
	}
	keys, err := s.queries.ListEnvironmentKeys(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "failed to list environment keys", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB.Error()).WithCause(ErrDB)
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
			slog.ErrorContext(ctx, "failed to load the environment key", "environmentId", environmentID, "error", err)
			return rewrapSkipped, connect.NewError(connect.CodeInternal, ErrDB.Error()).WithCause(ErrDB)
		}
		outcome, err := s.rewrapEnvironmentKey(ctx, key, newDEK)
		if errors.Is(err, errKeyChanged) {
			continue
		}
		return outcome, err
	}
	return rewrapSkipped, connect.NewError(connect.CodeAborted, ErrSecretKeyChanging.Error()).
		WithCause(ErrSecretKeyChanging)
}

func (s *SecretServer) rewrapEnvironmentKey(
	ctx context.Context,
	key genDb.EnvironmentKey,
	newDEK bool,
) (rewrapOutcome, error) {
	current, err := s.provider.KeyID(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "failed to read the current key id", "error", err)
		return rewrapSkipped, connect.NewError(connect.CodeUnavailable, ErrKeyProvider.Error()).
			WithCause(ErrKeyProvider)
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
		slog.ErrorContext(ctx, "unknown environment key format",
			"environmentId", key.EnvironmentID, "formatVersion", key.FormatVersion)
		return rewrapSkipped, connect.NewError(connect.CodeInternal, ErrUnknownSecretFormat.Error()).
			WithCause(ErrUnknownSecretFormat)
	}
	aad := secretkeys.DEKAAD(key.EnvironmentID.String())
	wrapped := secretkeys.WrappedKey{Provider: key.Provider, KeyID: key.KekID, Bytes: key.WrappedDek}
	dek, err := s.provider.Unwrap(ctx, wrapped, aad)
	if err != nil {
		slog.WarnContext(ctx, "cannot unwrap environment key", "environmentId", key.EnvironmentID, "kekId", key.KekID)
		return rewrapSkipped, nil
	}
	defer secretkeys.Zero(dek)
	replacement := dek
	if newDEK {
		replacement, err = secretkeys.NewDEK()
		if err != nil {
			slog.ErrorContext(ctx, "failed to generate a data key", "error", err)
			return rewrapSkipped, connect.NewError(connect.CodeInternal, ErrKeyProvider.Error()).
				WithCause(ErrKeyProvider)
		}
		defer secretkeys.Zero(replacement)
	}
	rewrapped, err := s.provider.Wrap(ctx, replacement, aad)
	if err != nil {
		slog.ErrorContext(ctx, "failed to wrap environment key", "environmentId", key.EnvironmentID, "error", err)
		return rewrapSkipped, connect.NewError(connect.CodeUnavailable, ErrKeyProvider.Error()).
			WithCause(ErrKeyProvider)
	}
	env, err := s.queries.GetEnvironmentByID(ctx, key.EnvironmentID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to load environment", "environmentId", key.EnvironmentID, "error", err)
		return rewrapSkipped, connect.NewError(connect.CodeInternal, ErrDB.Error()).WithCause(ErrDB)
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
			return updateErr
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
	if errors.Is(err, errKeyChanged) {
		return rewrapSkipped, err
	}
	if err != nil {
		return rewrapSkipped, txError(ctx, "failed to rewrap environment key", err)
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
		return err
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
		return err
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
		slog.ErrorContext(
			ctx,
			"unknown environment key format",
			"environmentId",
			environmentID,
			"formatVersion",
			key.FormatVersion,
		)
		return genDb.EnvironmentKey{}, nil, connect.NewError(connect.CodeInternal, ErrUnknownSecretFormat.Error()).
			WithCause(ErrUnknownSecretFormat)
	}
	dek, err := unwrapEnvironmentKey(ctx, s.provider, key)
	if err != nil {
		slog.ErrorContext(ctx, "failed to unwrap environment key", "environmentId", environmentID, "error", err)
		return genDb.EnvironmentKey{}, nil, connect.NewError(connect.CodeUnavailable, ErrKeyProvider.Error()).
			WithCause(ErrKeyProvider)
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
		slog.ErrorContext(ctx, "failed to generate a data key", "error", err)
		return genDb.EnvironmentKey{}, connect.NewError(connect.CodeInternal, ErrKeyProvider.Error()).
			WithCause(ErrKeyProvider)
	}
	defer secretkeys.Zero(dek)
	wrapped, err := s.provider.Wrap(ctx, dek, aad)
	if err != nil {
		slog.ErrorContext(ctx, "failed to wrap a data key", "environmentId", environmentID, "error", err)
		return genDb.EnvironmentKey{}, connect.NewError(connect.CodeUnavailable, ErrKeyProvider.Error()).
			WithCause(ErrKeyProvider)
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
		slog.ErrorContext(ctx, "failed to store the environment key", "environmentId", environmentID, "error", err)
		return genDb.EnvironmentKey{}, connect.NewError(connect.CodeInternal, ErrDB.Error()).WithCause(ErrDB)
	}
	return key, nil
}
