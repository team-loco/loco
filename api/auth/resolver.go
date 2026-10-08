package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
)

var ErrResolve = errors.New("could not resolve the signed-in user")

type Resolver struct {
	pool           *pgxpool.Pool
	queries        *genDb.Queries
	policy         SignupPolicy
	emailVerifiers EmailVerifiers
}

type ResolverOption func(*Resolver)

func WithEmailVerifiers(verifiers EmailVerifiers) ResolverOption {
	return func(r *Resolver) {
		r.emailVerifiers = verifiers
	}
}

func NewResolver(pool *pgxpool.Pool, policy SignupPolicy, opts ...ResolverOption) *Resolver {
	r := &Resolver{pool: pool, queries: genDb.New(pool), policy: policy}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

func (r *Resolver) Resolve(ctx context.Context, id Identity) (genDb.User, error) {
	user, err := r.existing(ctx, id)
	if err == nil {
		return user, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return genDb.User{}, err
	}

	verified, err := r.emailVerified(ctx, id, nil)
	if err != nil {
		slog.ErrorContext(ctx, "failed to check the email with the identity provider",
			"issuer", id.Issuer, "error", err)
		return genDb.User{}, ErrResolve
	}
	id.EmailVerified = verified
	user, err = r.provision(ctx, id)
	if isUniqueViolation(err) {
		return r.existing(ctx, id)
	}
	return user, err
}

func (r *Resolver) ssoCoversEmail(ctx context.Context, id Identity) (bool, error) {
	connection := id.SSOConnection()
	if connection == nil {
		return true, nil
	}
	covered, err := r.queries.SSOConnectionCoversDomain(ctx, genDb.SSOConnectionCoversDomainParams{
		ConnectionID: *connection,
		Issuer:       id.Issuer,
		Domain:       emailDomain(id.Email),
	})
	if err != nil {
		slog.ErrorContext(ctx, "failed to check the sso connection's domains", "error", err)
		return false, ErrResolve
	}
	if !covered {
		slog.WarnContext(ctx, "sso login asserted an email outside its organization's domains",
			"connection", *connection, "issuer", id.Issuer)
	}
	return covered, nil
}

func (r *Resolver) existing(ctx context.Context, id Identity) (genDb.User, error) {
	user, err := r.queries.GetUserByIdentity(ctx, genDb.GetUserByIdentityParams{
		Issuer:  id.Issuer,
		Subject: id.Subject,
	})
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			slog.ErrorContext(ctx, "failed to look up identity", "error", err)
			return genDb.User{}, ErrResolve
		}
		return genDb.User{}, err
	}
	verified, err := r.returningEmailVerified(ctx, id)
	if err != nil {
		return genDb.User{}, err
	}
	email := nullableString(id.Email)
	if err := r.queries.TouchIdentity(ctx, genDb.TouchIdentityParams{
		Issuer:        id.Issuer,
		Subject:       id.Subject,
		Email:         email,
		EmailVerified: verified,
	}); err != nil {
		slog.WarnContext(ctx, "failed to record identity login", "userId", user.ID, "error", err)
	}
	return user, nil
}

func (r *Resolver) returningEmailVerified(ctx context.Context, id Identity) (bool, error) {
	if _, ok := r.emailVerifiers[id.Issuer]; !ok {
		return r.emailVerified(ctx, id, nil)
	}
	stored, err := r.queries.GetIdentity(ctx, genDb.GetIdentityParams{Issuer: id.Issuer, Subject: id.Subject})
	if err != nil {
		slog.ErrorContext(ctx, "failed to load identity", "error", err)
		return false, ErrResolve
	}
	verified, err := r.emailVerified(ctx, id, &stored)
	if err != nil {
		slog.WarnContext(ctx, "failed to check the email with the identity provider; treating it as unverified",
			"issuer", id.Issuer, "error", err)
		return false, nil
	}
	return verified, nil
}

func (r *Resolver) emailVerified(ctx context.Context, id Identity, stored *genDb.Identity) (bool, error) {
	verified, err := r.providerEmailVerified(ctx, id, stored)
	if err != nil || !verified {
		return false, err
	}
	return r.ssoCoversEmail(ctx, id)
}

func (r *Resolver) providerEmailVerified(ctx context.Context, id Identity, stored *genDb.Identity) (bool, error) {
	lookup, ok := r.emailVerifiers[id.Issuer]
	if !ok {
		return id.EmailVerified, nil
	}
	if id.Email == "" {
		return false, nil
	}
	if stored != nil && stored.EmailVerified && stored.Email != nil && *stored.Email == id.Email {
		return true, nil
	}
	return lookup.EmailVerified(ctx, id.Subject, id.Email)
}

func (r *Resolver) provision(ctx context.Context, id Identity) (genDb.User, error) {
	if id.Email == "" {
		return genDb.User{}, ErrEmailMissing
	}
	if !id.EmailVerified {
		return genDb.User{}, ErrEmailUnverified
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "failed to begin transaction", "error", err)
		return genDb.User{}, ErrResolve
	}
	defer func() {
		if rbErr := tx.Rollback(ctx); rbErr != nil && !errors.Is(rbErr, pgx.ErrTxClosed) {
			slog.WarnContext(ctx, "failed to roll back provisioning", "error", rbErr)
		}
	}()
	qtx := r.queries.WithTx(tx)

	created := false
	user, err := qtx.GetUserByEmail(ctx, id.Email)
	switch {
	case err == nil:
		unverified, checkErr := qtx.UserHasUnverifiedIdentity(ctx, user.ID)
		if checkErr != nil {
			slog.ErrorContext(ctx, "failed to check the user's identities", "error", checkErr, "userId", user.ID)
			return genDb.User{}, ErrResolve
		}
		if unverified {
			return genDb.User{}, ErrEmailTaken
		}
	case errors.Is(err, pgx.ErrNoRows):
		if policyErr := r.policy.Check(id); policyErr != nil {
			return genDb.User{}, policyErr
		}
		user, err = createUser(ctx, qtx, id)
		if err != nil {
			return genDb.User{}, err
		}
		created = true
	default:
		slog.ErrorContext(ctx, "failed to look up user by email", "error", err)
		return genDb.User{}, ErrResolve
	}

	eventType := events.IdentityLinked
	if created {
		eventType = events.UserCreated
	}
	if err := events.RecordWith(ctx, qtx, events.Event{
		Type:        eventType,
		ActorType:   string(genDb.EntityTypeUser),
		ActorID:     new(user.ID),
		SubjectType: events.SubjectUser,
		SubjectID:   new(user.ID),
		Data:        map[string]any{"issuer": id.Issuer, "email": id.Email},
	}); err != nil {
		slog.ErrorContext(ctx, "failed to record identity event", "error", err)
		return genDb.User{}, ErrResolve
	}

	if _, err := qtx.CreateIdentity(ctx, genDb.CreateIdentityParams{
		UserID:        user.ID,
		Issuer:        id.Issuer,
		Subject:       id.Subject,
		Email:         nullableString(id.Email),
		EmailVerified: id.EmailVerified,
	}); err != nil {
		if isUniqueViolation(err) {
			return genDb.User{}, err
		}
		slog.ErrorContext(ctx, "failed to create identity", "error", err, "userId", user.ID)
		return genDb.User{}, ErrResolve
	}

	if id.EmailVerified {
		if _, joinErr := AutoJoin(ctx, qtx, user.ID, id.Email); joinErr != nil {
			slog.ErrorContext(ctx, "failed to apply domain auto-join", "error", joinErr, "userId", user.ID)
			return genDb.User{}, ErrResolve
		}
	}

	if err := tx.Commit(ctx); err != nil {
		if isUniqueViolation(err) {
			return genDb.User{}, err
		}
		slog.ErrorContext(ctx, "failed to commit provisioning", "error", err)
		return genDb.User{}, ErrResolve
	}
	slog.InfoContext(ctx, "provisioned identity", "userId", user.ID, "issuer", id.Issuer)
	return user, nil
}

func createUser(ctx context.Context, qtx *genDb.Queries, id Identity) (genDb.User, error) {
	user, err := qtx.CreateUser(ctx, genDb.CreateUserParams{
		Email:     id.Email,
		Name:      nullableString(id.Name),
		AvatarUrl: nullableString(id.AvatarURL),
	})
	if err != nil {
		if isUniqueViolation(err) {
			return genDb.User{}, err
		}
		slog.ErrorContext(ctx, "failed to create user", "error", err)
		return genDb.User{}, ErrResolve
	}
	for _, scope := range []genDb.Scope{genDb.ScopeRead, genDb.ScopeWrite, genDb.ScopeAdmin} {
		if err := qtx.AddUserScope(ctx, genDb.AddUserScopeParams{
			UserID:     user.ID,
			EntityType: genDb.EntityTypeUser,
			EntityID:   user.ID,
			Scope:      scope,
		}); err != nil {
			slog.ErrorContext(ctx, "failed to grant user scope", "error", err, "userId", user.ID)
			return genDb.User{}, fmt.Errorf("%w: %w", ErrResolve, err)
		}
	}
	return user, nil
}

func nullableString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
