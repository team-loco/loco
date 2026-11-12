package auth

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
)

var ErrBootstrapIdentityInactive = errors.New("bootstrap requires an active account")

// BootstrapUser resolves a verified identity inside the caller's one-shot bootstrap
// transaction. The installation proof authorizes creating this user under closed signup.
func (r *Resolver) BootstrapUser(
	ctx context.Context,
	queries *genDb.Queries,
	id Identity,
) (genDb.User, genDb.Identity, error) {
	verified, err := r.emailVerified(ctx, id, nil)
	if err != nil {
		return genDb.User{}, genDb.Identity{}, fmt.Errorf("verify bootstrap email: %w", err)
	}
	if id.Email == "" {
		return genDb.User{}, genDb.Identity{}, ErrEmailMissing
	}
	if !verified {
		return genDb.User{}, genDb.Identity{}, ErrEmailUnverified
	}
	row, err := queries.GetUserByIdentity(ctx, genDb.GetUserByIdentityParams{Issuer: id.Issuer, Subject: id.Subject})
	if err == nil {
		if row.User.Status != genDb.UserStatusActive {
			return genDb.User{}, genDb.Identity{}, ErrBootstrapIdentityInactive
		}
		identity, lookupErr := queries.GetIdentity(ctx, genDb.GetIdentityParams{Issuer: id.Issuer, Subject: id.Subject})
		if lookupErr != nil {
			return genDb.User{}, genDb.Identity{}, fmt.Errorf("look up bootstrap identity: %w", lookupErr)
		}
		return row.User, identity, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return genDb.User{}, genDb.Identity{}, fmt.Errorf("look up bootstrap identity: %w", err)
	}
	_, err = queries.GetUserByEmail(ctx, id.Email)
	if err == nil {
		return genDb.User{}, genDb.Identity{}, ErrEmailTaken
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return genDb.User{}, genDb.Identity{}, fmt.Errorf("look up bootstrap email: %w", err)
	}
	id.EmailVerified = true
	user, err := CreateUser(ctx, queries, genDb.CreateUserParams{
		Email: id.Email, Name: nullableString(id.Name), AvatarUrl: nullableString(id.AvatarURL),
	})
	if err != nil {
		return genDb.User{}, genDb.Identity{}, err
	}
	identity, err := queries.CreateIdentity(ctx, genDb.CreateIdentityParams{
		UserID: user.ID, Issuer: id.Issuer, Subject: id.Subject,
		Email: &id.Email, EmailVerified: true,
	})
	if err != nil {
		return genDb.User{}, genDb.Identity{}, fmt.Errorf("create bootstrap identity: %w", err)
	}
	if err = events.Record(ctx, queries, events.Event{
		Type: events.UserCreated, ActorType: string(genDb.EntityTypeUser), ActorID: new(user.ID),
		SubjectType: events.SubjectUser, SubjectID: new(user.ID),
		Data: map[string]any{events.FieldIssuer: id.Issuer, events.FieldEmail: id.Email},
	}); err != nil {
		return genDb.User{}, genDb.Identity{}, fmt.Errorf("record bootstrap user: %w", err)
	}
	return user, identity, nil
}
