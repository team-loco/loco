package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
)

var ErrResolve = errors.New("could not resolve the signed-in user")

type ClaimedOrg struct {
	OrgID       uuid.UUID
	OrgName     string
	InviterName string
}

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

func (r *Resolver) Resolve(ctx context.Context, id Identity) (genDb.User, []ClaimedOrg, error) {
	user, claimed, err := r.existing(ctx, id)
	if err == nil {
		return user, claimed, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return genDb.User{}, nil, err
	}

	verified, err := r.emailVerified(ctx, id, nil)
	if err != nil {
		slog.ErrorContext(ctx, "failed to check the email with the identity provider",
			"issuer", id.Issuer, "error", err)
		return genDb.User{}, nil, ErrResolve
	}
	id.EmailVerified = verified
	user, claimed, err = r.provision(ctx, id)
	if isUniqueViolation(err) {
		return r.existing(ctx, id)
	}
	return user, claimed, err
}

func (r *Resolver) existing(ctx context.Context, id Identity) (genDb.User, []ClaimedOrg, error) {
	row, err := r.queries.GetUserByIdentity(ctx, genDb.GetUserByIdentityParams{
		Issuer:  id.Issuer,
		Subject: id.Subject,
	})
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			slog.ErrorContext(ctx, "failed to look up identity", "error", err)
			return genDb.User{}, nil, ErrResolve
		}
		return genDb.User{}, nil, err
	}
	user := row.User
	verified, err := r.returningEmailVerified(ctx, id)
	if err != nil {
		return genDb.User{}, nil, err
	}
	email := nullableString(id.Email)
	storedVerified := row.IdentityEmailVerified && row.IdentityEmail != nil && *row.IdentityEmail == id.Email
	if verified && email != nil && !storedVerified {
		user, err = r.recordVerifiedEmail(ctx, user, id)
		if err != nil {
			return genDb.User{}, nil, err
		}
	} else if touchErr := r.queries.TouchIdentity(ctx, genDb.TouchIdentityParams{
		Issuer:        id.Issuer,
		Subject:       id.Subject,
		Email:         email,
		EmailVerified: verified,
	}); touchErr != nil {
		slog.WarnContext(ctx, "failed to record identity login", "userId", user.ID, "error", touchErr)
	}
	if !verified || email == nil {
		return user, nil, nil
	}
	claimed, err := r.claimOpenInvitations(ctx, user, id.Email)
	if err != nil {
		return genDb.User{}, nil, err
	}
	return user, claimed, nil
}

func (r *Resolver) claimOpenInvitations(ctx context.Context, user genDb.User, email string) ([]ClaimedOrg, error) {
	invitations, err := r.queries.ListOpenInvitationsForEmail(ctx, email)
	if err != nil {
		slog.ErrorContext(ctx, "failed to look up invitations", "error", err, "userId", user.ID)
		return nil, ErrResolve
	}
	if len(invitations) == 0 {
		return nil, nil
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "failed to begin transaction", "error", err)
		return nil, ErrResolve
	}
	defer func() {
		if rbErr := tx.Rollback(ctx); rbErr != nil && !errors.Is(rbErr, pgx.ErrTxClosed) {
			slog.WarnContext(ctx, "failed to roll back invitation claim", "error", rbErr)
		}
	}()
	claimed, claimErr := claimInvitations(ctx, r.queries.WithTx(tx), user, invitations)
	if claimErr != nil {
		slog.ErrorContext(ctx, "failed to claim invitations", "error", claimErr, "userId", user.ID)
		return nil, ErrResolve
	}
	if commitErr := tx.Commit(ctx); commitErr != nil {
		slog.ErrorContext(ctx, "failed to commit invitation claim", "error", commitErr, "userId", user.ID)
		return nil, ErrResolve
	}
	return claimed, nil
}

func claimInvitations(
	ctx context.Context,
	qtx *genDb.Queries,
	user genDb.User,
	invitations []genDb.Invitation,
) ([]ClaimedOrg, error) {
	if user.Status != genDb.UserStatusActive {
		return nil, nil
	}
	var claimedOrgs []ClaimedOrg
	for _, invitation := range invitations {
		claimed, err := qtx.ClaimInvitation(ctx, genDb.ClaimInvitationParams{ID: invitation.ID, UserID: user.ID})
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("claim invitation %s: %w", invitation.ID, err)
		}
		if scopeErr := qtx.AddUserScope(ctx, genDb.AddUserScopeParams{
			UserID:     user.ID,
			EntityType: genDb.EntityTypeOrganization,
			EntityID:   claimed.OrgID,
			Scope:      genDb.ScopeMember,
		}); scopeErr != nil {
			return nil, fmt.Errorf("add member scope for invitation %s: %w", claimed.ID, scopeErr)
		}
		if eventErr := events.Record(ctx, qtx, events.Event{
			Type:        events.OrgMemberAdded,
			OrgID:       new(claimed.OrgID),
			ActorType:   string(genDb.EntityTypeUser),
			ActorID:     new(user.ID),
			SubjectType: events.SubjectUser,
			SubjectID:   new(user.ID),
		}); eventErr != nil {
			return nil, fmt.Errorf("record member added for invitation %s: %w", claimed.ID, eventErr)
		}
		if eventErr := events.Record(ctx, qtx, events.Event{
			Type:        events.InvitationClaimed,
			OrgID:       new(claimed.OrgID),
			ActorType:   string(genDb.EntityTypeUser),
			ActorID:     new(user.ID),
			SubjectType: events.SubjectInvitation,
			SubjectID:   new(claimed.ID),
			Data:        map[string]any{events.FieldEmail: claimed.Email},
		}); eventErr != nil {
			return nil, fmt.Errorf("record invitation %s claimed: %w", claimed.ID, eventErr)
		}
		details, detailsErr := qtx.GetInvitationClaimDetails(ctx, claimed.ID)
		if detailsErr != nil {
			return nil, fmt.Errorf("load invitation %s details: %w", claimed.ID, detailsErr)
		}
		claimedOrgs = append(claimedOrgs, ClaimedOrg{
			OrgID:       claimed.OrgID,
			OrgName:     details.OrgName,
			InviterName: details.InviterName,
		})
	}
	return claimedOrgs, nil
}

func (r *Resolver) recordVerifiedEmail(ctx context.Context, user genDb.User, id Identity) (genDb.User, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "failed to begin transaction", "error", err)
		return genDb.User{}, ErrResolve
	}
	defer func() {
		if rbErr := tx.Rollback(ctx); rbErr != nil && !errors.Is(rbErr, pgx.ErrTxClosed) {
			slog.WarnContext(ctx, "failed to roll back email verification", "error", rbErr)
		}
	}()
	qtx := r.queries.WithTx(tx)
	if touchErr := qtx.TouchIdentity(ctx, genDb.TouchIdentityParams{
		Issuer:        id.Issuer,
		Subject:       id.Subject,
		Email:         &id.Email,
		EmailVerified: true,
	}); touchErr != nil {
		slog.ErrorContext(ctx, "failed to record the verified email", "error", touchErr, "userId", user.ID)
		return genDb.User{}, ErrResolve
	}
	user, err = moveAccountEmail(ctx, qtx, user, id.Email)
	if err != nil {
		return genDb.User{}, err
	}
	if commitErr := tx.Commit(ctx); commitErr != nil {
		slog.ErrorContext(ctx, "failed to commit the verified email", "error", commitErr, "userId", user.ID)
		return genDb.User{}, ErrResolve
	}
	return user, nil
}

func moveAccountEmail(ctx context.Context, qtx *genDb.Queries, user genDb.User, email string) (genDb.User, error) {
	if user.Email == email {
		return user, nil
	}
	holder, err := qtx.GetUserByEmail(ctx, email)
	if err == nil {
		slog.WarnContext(ctx, "kept the account email: the identity's new verified address belongs to another user",
			"userId", user.ID, "otherUserId", holder.ID)
		return user, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		slog.ErrorContext(ctx, "failed to look up user by email", "error", err, "userId", user.ID)
		return genDb.User{}, ErrResolve
	}
	moved, err := qtx.MoveAccountEmail(ctx, genDb.MoveAccountEmailParams{UserID: user.ID, Email: email})
	if errors.Is(err, pgx.ErrNoRows) {
		return user, nil
	}
	if err != nil {
		slog.ErrorContext(ctx, "failed to move the account email", "error", err, "userId", user.ID)
		return genDb.User{}, ErrResolve
	}
	if err := events.Record(ctx, qtx, events.Event{
		Type:        events.UserUpdated,
		ActorType:   string(genDb.EntityTypeUser),
		ActorID:     new(user.ID),
		SubjectType: events.SubjectUser,
		SubjectID:   new(user.ID),
		Data:        map[string]any{events.FieldEmail: email},
	}); err != nil {
		slog.ErrorContext(ctx, "failed to record the account email change", "error", err, "userId", user.ID)
		return genDb.User{}, ErrResolve
	}
	slog.InfoContext(ctx, "moved the account email to the identity's verified address", "userId", user.ID)
	return moved, nil
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

func (r *Resolver) provision(ctx context.Context, id Identity) (genDb.User, []ClaimedOrg, error) {
	if id.Email == "" {
		return genDb.User{}, nil, ErrEmailMissing
	}
	if !id.EmailVerified {
		return genDb.User{}, nil, ErrEmailUnverified
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		slog.ErrorContext(ctx, "failed to begin transaction", "error", err)
		return genDb.User{}, nil, ErrResolve
	}
	defer func() {
		if rbErr := tx.Rollback(ctx); rbErr != nil && !errors.Is(rbErr, pgx.ErrTxClosed) {
			slog.WarnContext(ctx, "failed to roll back provisioning", "error", rbErr)
		}
	}()
	qtx := r.queries.WithTx(tx)

	invitations, err := qtx.ListOpenInvitationsForEmail(ctx, id.Email)
	if err != nil {
		slog.ErrorContext(ctx, "failed to look up invitations", "error", err)
		return genDb.User{}, nil, ErrResolve
	}

	created := false
	user, err := qtx.GetUserByEmail(ctx, id.Email)
	switch {
	case err == nil:
		unverified, checkErr := qtx.UserHasUnverifiedIdentity(ctx, user.ID)
		if checkErr != nil {
			slog.ErrorContext(ctx, "failed to check the user's identities", "error", checkErr, "userId", user.ID)
			return genDb.User{}, nil, ErrResolve
		}
		if unverified {
			return genDb.User{}, nil, ErrEmailTaken
		}
	case errors.Is(err, pgx.ErrNoRows):
		if policyErr := r.policy.Check(id); policyErr != nil {
			return genDb.User{}, nil, policyErr
		}
		user, err = createUser(ctx, qtx, id)
		if err != nil {
			return genDb.User{}, nil, err
		}
		created = true
	default:
		slog.ErrorContext(ctx, "failed to look up user by email", "error", err)
		return genDb.User{}, nil, ErrResolve
	}

	eventType := events.IdentityLinked
	if created {
		eventType = events.UserCreated
	}
	if err := events.Record(ctx, qtx, events.Event{
		Type:        eventType,
		ActorType:   string(genDb.EntityTypeUser),
		ActorID:     new(user.ID),
		SubjectType: events.SubjectUser,
		SubjectID:   new(user.ID),
		Data:        map[string]any{"issuer": id.Issuer, events.FieldEmail: id.Email},
	}); err != nil {
		slog.ErrorContext(ctx, "failed to record identity event", "error", err)
		return genDb.User{}, nil, ErrResolve
	}

	if _, err := qtx.CreateIdentity(ctx, genDb.CreateIdentityParams{
		UserID:        user.ID,
		Issuer:        id.Issuer,
		Subject:       id.Subject,
		Email:         nullableString(id.Email),
		EmailVerified: id.EmailVerified,
	}); err != nil {
		if isUniqueViolation(err) {
			return genDb.User{}, nil, err
		}
		slog.ErrorContext(ctx, "failed to create identity", "error", err, "userId", user.ID)
		return genDb.User{}, nil, ErrResolve
	}

	claimed, claimErr := claimInvitations(ctx, qtx, user, invitations)
	if claimErr != nil {
		slog.ErrorContext(ctx, "failed to claim invitations", "error", claimErr, "userId", user.ID)
		return genDb.User{}, nil, ErrResolve
	}

	if err := tx.Commit(ctx); err != nil {
		if isUniqueViolation(err) {
			return genDb.User{}, nil, err
		}
		slog.ErrorContext(ctx, "failed to commit provisioning", "error", err)
		return genDb.User{}, nil, ErrResolve
	}
	slog.InfoContext(ctx, "provisioned identity", "userId", user.ID, "issuer", id.Issuer)
	return user, claimed, nil
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
