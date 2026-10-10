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

	"connectrpc.com/connect"
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
	ErrSecretsNotConfigured = errors.New("no secrets key provider is configured")
	ErrSecretValueTooLarge  = errors.New("secret value exceeds the size limit")
	ErrTooManySecrets       = errors.New("environment has reached its secret limit")
	ErrSecretNotFound       = errors.New("secret not found")
	ErrKeyProvider          = errors.New("secrets key provider unavailable")
	ErrSecretLockTimeout    = errors.New("secrets of the environment are locked by another operation")
	ErrSecretKeyChanging    = errors.New("environment key changed during the write, retry")
	ErrServiceSecretsLarge  = errors.New("secrets of the service exceed the size limit")
	ErrSecretInUse          = errors.New("secret is declared by a service")
	ErrUnknownSecretFormat  = errors.New("secret has a format version this binary does not know")

	errKeyChanged = errors.New("environment key changed under the lock")
)

const secretFormatVersion int16 = 1

const (
	secretTagBytes     = 16
	setSecretsAttempts = 3
)

// SecretConfig holds the limits SetSecrets enforces and how long a secret operation waits for
// the environment's key row lock.
type SecretConfig struct {
	MaxValueBytes     int32
	MaxPerEnvironment int32
	MaxServiceBytes   int32
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

func (s *SecretServer) authorizeEnvironment(
	ctx context.Context,
	environmentID string,
	action actions.Action,
) (genDb.Environment, error) {
	scopes, ok := ctx.Value(contextkeys.EntityScopesKey).([]genDb.EntityScope)
	if !ok {
		slog.ErrorContext(ctx, "entity scopes not found in context")
		return genDb.Environment{}, connect.NewError(connect.CodeInternal, errEntityScopesNotFound)
	}
	env, err := s.queries.GetEnvironmentByID(ctx, uuid.MustParse(environmentID))
	if errors.Is(err, pgx.ErrNoRows) {
		return genDb.Environment{}, connect.NewError(connect.CodeNotFound, ErrEnvironmentNotFound)
	}
	if err != nil {
		slog.ErrorContext(ctx, "failed to load environment", "error", err)
		return genDb.Environment{}, connect.NewError(connect.CodeInternal, ErrDB)
	}
	required := actions.New(action, env.WorkspaceID.String())
	if err := s.authz.Check(ctx, scopes, required); err != nil {
		slog.WarnContext(ctx, "unauthorized secret access", "environmentId", environmentID)
		return genDb.Environment{}, connect.NewError(connect.CodePermissionDenied, err)
	}
	return env, nil
}

// SetSecrets encrypts each value under the environment's data key and stores it. A value equal
// to the stored one changes nothing.
func (s *SecretServer) SetSecrets(
	ctx context.Context,
	req *connect.Request[secretv1.SetSecretsRequest],
) (*connect.Response[secretv1.SetSecretsResponse], error) {
	r := req.Msg
	env, err := s.authorizeEnvironment(ctx, r.GetEnvironmentId(), actions.SetSecrets)
	if err != nil {
		return nil, err
	}
	entity, ok := ctx.Value(contextkeys.EntityKey).(genDb.Entity)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, ErrUnauthorized)
	}
	if s.provider == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrSecretsNotConfigured)
	}
	values := r.GetValues()
	names := slices.Sorted(maps.Keys(values))
	for _, name := range names {
		if int64(len(values[name])) > int64(s.config.MaxValueBytes) {
			tooLarge := fmt.Errorf("%w: %s", ErrSecretValueTooLarge, name)
			return nil, connect.NewError(connect.CodeInvalidArgument, tooLarge)
		}
	}

	for range setSecretsAttempts {
		response, setErr := s.setSecretsOnce(ctx, env, entity, names, values)
		if errors.Is(setErr, errKeyChanged) {
			continue
		}
		if setErr != nil {
			return nil, setErr
		}
		return connect.NewResponse(response), nil
	}
	return nil, connect.NewError(connect.CodeAborted, ErrSecretKeyChanging)
}

func (s *SecretServer) setSecretsOnce(
	ctx context.Context,
	env genDb.Environment,
	updatedBy genDb.Entity,
	names []string,
	values map[string]string,
) (*secretv1.SetSecretsResponse, error) {
	key, dek, err := s.environmentDEK(ctx, env.ID)
	if err != nil {
		return nil, err
	}
	defer secretkeys.Zero(dek)

	response := &secretv1.SetSecretsResponse{Versions: make([]*secretv1.SecretVersion, 0, len(names))}
	err = withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		locked, lockErr := s.lockEnvironmentKey(ctx, qtx, env.ID)
		if lockErr != nil {
			return lockErr
		}
		if !bytes.Equal(locked.WrappedDek, key.WrappedDek) {
			return errKeyChanged
		}
		existing, listErr := qtx.ListSecretCiphertexts(ctx, genDb.ListSecretCiphertextsParams{
			EnvironmentID: env.ID, Names: names,
		})
		if listErr != nil {
			return listErr
		}
		current := make(map[string]genDb.ListSecretCiphertextsRow, len(existing))
		for _, row := range existing {
			current[row.Name] = row
		}

		params := genDb.UpsertSecretsParams{
			EnvironmentID: env.ID,
			UpdatedByType: string(updatedBy.Type),
			UpdatedByID:   updatedBy.ID,
		}
		added := int64(0)
		for _, name := range names {
			row, found := current[name]
			if found {
				same, sameErr := storedValueEquals(dek, env, name, row, values[name])
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
			aad := secretkeys.SecretAAD(env.WorkspaceID.String(), env.ID.String(), name, version)
			nonce, ciphertext, sealErr := secretkeys.Seal(dek, []byte(values[name]), aad)
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
			unchanged, getErr := qtx.GetEnvironmentByID(ctx, env.ID)
			if getErr != nil {
				return getErr
			}
			response.Revision = unchanged.Revision
			return nil
		}
		return s.writeSecrets(ctx, qtx, env, params, added, response)
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
	env genDb.Environment,
	params genDb.UpsertSecretsParams,
	added int64,
	response *secretv1.SetSecretsResponse,
) error {
	count, countErr := qtx.CountSecrets(ctx, env.ID)
	if countErr != nil {
		return countErr
	}
	if count+added > int64(s.config.MaxPerEnvironment) {
		return connect.NewError(connect.CodeResourceExhausted, ErrTooManySecrets)
	}
	if upsertErr := qtx.UpsertSecrets(ctx, params); upsertErr != nil {
		return upsertErr
	}
	sizes, sizesErr := qtx.ListServiceSecretSizes(ctx, genDb.ListServiceSecretSizesParams{
		EnvironmentID: env.ID, Names: params.Names,
	})
	if sizesErr != nil {
		return sizesErr
	}
	for _, size := range sizes {
		total := size.NameBytes + size.CiphertextBytes - size.SecretCount*secretTagBytes
		if total > int64(s.config.MaxServiceBytes) {
			tooLarge := fmt.Errorf("%w: %s", ErrServiceSecretsLarge, size.Service)
			return connect.NewError(connect.CodeFailedPrecondition, tooLarge)
		}
	}
	revision, bumpErr := bumpEnvironmentRevision(ctx, qtx, env.ID)
	if bumpErr != nil {
		return bumpErr
	}
	response.Revision = revision
	return events.Record(ctx, qtx, events.Event{
		Type:        events.SecretSet,
		WorkspaceID: new(env.WorkspaceID),
		SubjectType: events.SubjectEnvironment,
		SubjectID:   new(env.ID),
		Data:        map[string]any{events.FieldNames: params.Names, events.FieldRevision: revision},
	})
}

func storedValueEquals(
	dek []byte,
	env genDb.Environment,
	name string,
	row genDb.ListSecretCiphertextsRow,
	value string,
) (bool, error) {
	if row.FormatVersion != secretFormatVersion {
		return false, fmt.Errorf("%w: %s has %d", ErrUnknownSecretFormat, name, row.FormatVersion)
	}
	aad := secretkeys.SecretAAD(env.WorkspaceID.String(), env.ID.String(), name, row.Version)
	plaintext, err := secretkeys.Open(dek, row.Nonce, row.Ciphertext, aad)
	if err != nil {
		return false, fmt.Errorf("open %s: %w", name, err)
	}
	defer secretkeys.Zero(plaintext)
	return subtle.ConstantTimeCompare(plaintext, []byte(value)) == 1, nil
}

func (s *SecretServer) lockEnvironmentKey(
	ctx context.Context,
	qtx *genDb.Queries,
	environmentID uuid.UUID,
) (genDb.EnvironmentKey, error) {
	timeoutMillis := strconv.FormatInt(s.config.LockTimeout.Milliseconds(), 10)
	if err := qtx.SetLockTimeout(ctx, timeoutMillis); err != nil {
		return genDb.EnvironmentKey{}, err
	}
	key, err := qtx.LockEnvironmentKey(ctx, environmentID)
	if isPgLockNotAvailable(err) {
		return genDb.EnvironmentKey{}, connect.NewError(connect.CodeAborted, ErrSecretLockTimeout)
	}
	return key, err
}

// DeleteSecrets removes names from an environment.
func (s *SecretServer) DeleteSecrets(
	ctx context.Context,
	req *connect.Request[secretv1.DeleteSecretsRequest],
) (*connect.Response[secretv1.DeleteSecretsResponse], error) {
	r := req.Msg
	env, err := s.authorizeEnvironment(ctx, r.GetEnvironmentId(), actions.DeleteSecrets)
	if err != nil {
		return nil, err
	}
	names := slices.Sorted(slices.Values(r.GetNames()))
	var revision int64
	err = withTx(ctx, s.db, func(qtx *genDb.Queries) error {
		if _, lockErr := s.lockEnvironmentKey(ctx, qtx, env.ID); lockErr != nil {
			if errors.Is(lockErr, pgx.ErrNoRows) {
				return connect.NewError(connect.CodeNotFound, fmt.Errorf("%w: %s", ErrSecretNotFound, names[0]))
			}
			return lockErr
		}
		services, servicesErr := qtx.ListServicesDeclaringSecrets(ctx, genDb.ListServicesDeclaringSecretsParams{
			EnvironmentID: env.ID, Names: names,
		})
		if servicesErr != nil {
			return servicesErr
		}
		if len(services) > 0 {
			inUse := fmt.Errorf("%w: %s", ErrSecretInUse, strings.Join(services, ", "))
			return connect.NewError(connect.CodeFailedPrecondition, inUse)
		}
		deleted, deleteErr := qtx.DeleteSecrets(ctx, genDb.DeleteSecretsParams{EnvironmentID: env.ID, Names: names})
		if deleteErr != nil {
			return deleteErr
		}
		if len(deleted) != len(names) {
			slices.Sort(deleted)
			for _, name := range names {
				if _, found := slices.BinarySearch(deleted, name); !found {
					return connect.NewError(connect.CodeNotFound, fmt.Errorf("%w: %s", ErrSecretNotFound, name))
				}
			}
		}
		bumped, bumpErr := bumpEnvironmentRevision(ctx, qtx, env.ID)
		if bumpErr != nil {
			return bumpErr
		}
		revision = bumped
		return events.Record(ctx, qtx, events.Event{
			Type:        events.SecretDeleted,
			WorkspaceID: new(env.WorkspaceID),
			SubjectType: events.SubjectEnvironment,
			SubjectID:   new(env.ID),
			Data:        map[string]any{events.FieldNames: names, events.FieldRevision: revision},
		})
	})
	if err != nil {
		return nil, txError(ctx, "failed to delete secrets", err)
	}
	return connect.NewResponse(&secretv1.DeleteSecretsResponse{Revision: revision}), nil
}

// ListSecrets lists the names in an environment; it never returns a value.
func (s *SecretServer) ListSecrets(
	ctx context.Context,
	req *connect.Request[secretv1.ListSecretsRequest],
) (*connect.Response[secretv1.ListSecretsResponse], error) {
	env, err := s.authorizeEnvironment(ctx, req.Msg.GetEnvironmentId(), actions.ListSecrets)
	if err != nil {
		return nil, err
	}
	rows, err := s.queries.ListSecrets(ctx, env.ID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to list secrets", "error", err)
		return nil, connect.NewError(connect.CodeInternal, ErrDB)
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
	return connect.NewResponse(&secretv1.ListSecretsResponse{Secrets: secrets}), nil
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
		return genDb.EnvironmentKey{}, nil, connect.NewError(connect.CodeInternal, ErrUnknownSecretFormat)
	}
	wrapped := secretkeys.WrappedKey{Provider: key.Provider, KeyID: key.KekID, Bytes: key.WrappedDek}
	dek, err := s.provider.Unwrap(ctx, wrapped, aad)
	if err != nil {
		slog.ErrorContext(ctx, "failed to unwrap environment key", "environmentId", environmentID, "error", err)
		return genDb.EnvironmentKey{}, nil, connect.NewError(connect.CodeUnavailable, ErrKeyProvider)
	}
	return key, dek, nil
}

func (s *SecretServer) createEnvironmentKey(
	ctx context.Context,
	environmentID uuid.UUID,
	aad []byte,
) (genDb.EnvironmentKey, error) {
	dek, err := secretkeys.NewDEK()
	if err != nil {
		slog.ErrorContext(ctx, "failed to generate a data key", "error", err)
		return genDb.EnvironmentKey{}, connect.NewError(connect.CodeInternal, ErrKeyProvider)
	}
	defer secretkeys.Zero(dek)
	wrapped, err := s.provider.Wrap(ctx, dek, aad)
	if err != nil {
		slog.ErrorContext(ctx, "failed to wrap a data key", "environmentId", environmentID, "error", err)
		return genDb.EnvironmentKey{}, connect.NewError(connect.CodeUnavailable, ErrKeyProvider)
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
		return genDb.EnvironmentKey{}, connect.NewError(connect.CodeInternal, ErrDB)
	}
	return key, nil
}
