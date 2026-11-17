package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/mail"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/team-loco/loco/api/authz"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
)

var (
	errInvalidAdminEmail  = errors.New("not a bare email address")
	errAdminClaimsChanged = errors.New("system admin claims changed while locking accounts")
)

// SystemAdmins lists the configured system-admin emails, lowercased.
type SystemAdmins []string

func ParseSystemAdmins(entries []string) (SystemAdmins, error) {
	var admins SystemAdmins
	for _, entry := range entries {
		address, err := mail.ParseAddress(entry)
		if err != nil || address.Name != "" || address.Address != entry {
			return nil, fmt.Errorf("%q: %w", entry, errInvalidAdminEmail)
		}
		email := strings.ToLower(entry)
		if !slices.Contains(admins, email) {
			admins = append(admins, email)
		}
	}
	return admins, nil
}

func (a SystemAdmins) lists(email string) bool {
	return email != "" && slices.Contains(a, strings.ToLower(email))
}

// SyncSystemAdmins makes the claim table match admins: it opens a claim for each new email and
// deletes the claims of removed emails, revoking system:admin from the users who claimed them
// unless GrantSystemAdmin also granted it.
func SyncSystemAdmins(ctx context.Context, pool *pgxpool.Pool, admins SystemAdmins) error {
	emails := []string(admins)
	if emails == nil {
		emails = []string{}
	}
	for {
		err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
			q := genDb.New(tx)
			entries, err := q.ListSystemAdminEntriesExcept(ctx, emails)
			if err != nil {
				return fmt.Errorf("list removed system admin entries: %w", err)
			}
			var users []uuid.UUID
			for _, entry := range entries {
				if entry.UserID != nil {
					users = append(users, *entry.UserID)
				}
			}
			if lockErr := authz.LockUsers(ctx, q, users...); errors.Is(lockErr, pgx.ErrNoRows) {
				return errAdminClaimsChanged
			} else if lockErr != nil {
				return fmt.Errorf("lock system admin accounts: %w", lockErr)
			}
			locked, err := q.LockSystemAdminEntriesExcept(ctx, emails)
			if err != nil {
				return fmt.Errorf("lock removed system admin entries: %w", err)
			}
			for _, entry := range locked {
				if entry.UserID != nil && !slices.Contains(users, *entry.UserID) {
					// A claim committed after the account snapshot. Retry before
					// changing anything, preserving user-before-claim lock order.
					return errAdminClaimsChanged
				}
			}
			removed, err := q.RemoveSystemAdminEntriesExcept(ctx, emails)
			if err != nil {
				return fmt.Errorf("remove system admin entries: %w", err)
			}
			for _, entry := range removed {
				if entry.UserID == nil {
					continue
				}
				if err := revokeSystemAdmin(ctx, q, entry); err != nil {
					return err
				}
			}
			if err := q.AddSystemAdminEntries(ctx, emails); err != nil {
				return fmt.Errorf("add system admin entries: %w", err)
			}
			return nil
		})
		if !errors.Is(err, errAdminClaimsChanged) {
			return err
		}
	}
}

func revokeSystemAdmin(ctx context.Context, q *genDb.Queries, entry genDb.RemoveSystemAdminEntriesExceptRow) error {
	if err := q.RemoveSystemAdminScopeUnlessGranted(ctx, *entry.UserID); err != nil {
		return fmt.Errorf("revoke system admin from user %s: %w", *entry.UserID, err)
	}
	data := map[string]any{events.FieldEmail: entry.Email, events.FieldSource: events.SourceConfig}
	if entry.Issuer != nil && entry.Subject != nil {
		data[events.FieldIssuer] = *entry.Issuer
		data[events.FieldSubject] = *entry.Subject
	}
	if err := events.Record(ctx, q, events.Event{
		Type:        events.SystemAdminRevoked,
		ActorType:   events.ActorSystem,
		SubjectType: events.SubjectUser,
		SubjectID:   entry.UserID,
		Data:        data,
	}); err != nil {
		return fmt.Errorf("record system admin revoked for user %s: %w", *entry.UserID, err)
	}
	slog.InfoContext(ctx, "revoked system admin removed from configuration", "userId", *entry.UserID)
	return nil
}

// claimSystemAdmin grants system:admin to user when id's verified email is an unclaimed entry.
func (r *Resolver) claimSystemAdmin(
	ctx context.Context,
	qtx *genDb.Queries,
	user genDb.User,
	id Identity,
) (bool, error) {
	if !id.EmailVerified || !r.systemAdmins.lists(id.Email) || user.Status != genDb.UserStatusActive {
		return false, nil
	}
	var err error
	user, err = lockActiveUser(ctx, qtx, user.ID)
	if err != nil {
		return false, err
	}
	email := strings.ToLower(id.Email)
	claimed, err := qtx.ClaimSystemAdmin(ctx, genDb.ClaimSystemAdminParams{UserID: user.ID, Email: email})
	if err != nil {
		return false, fmt.Errorf("%w: claim system admin for user %s: %w", ErrResolve, user.ID, err)
	}
	if claimed == 0 {
		return false, nil
	}
	if err := qtx.AddUserScope(ctx, SystemAdminScope(user.ID)); err != nil {
		return false, fmt.Errorf("%w: grant system admin to user %s: %w", ErrResolve, user.ID, err)
	}
	if err := events.Record(ctx, qtx, events.Event{
		Type:        events.SystemAdminClaimed,
		ActorType:   string(genDb.EntityTypeUser),
		ActorID:     new(user.ID),
		SubjectType: events.SubjectUser,
		SubjectID:   new(user.ID),
		Data: map[string]any{
			events.FieldEmail:   email,
			events.FieldIssuer:  id.Issuer,
			events.FieldSubject: id.Subject,
			events.FieldSource:  events.SourceConfig,
		},
	}); err != nil {
		return false, fmt.Errorf("%w: record system admin claimed for user %s: %w", ErrResolve, user.ID, err)
	}
	slog.InfoContext(ctx, "granted system admin from configuration", "userId", user.ID, "issuer", id.Issuer)
	return true, nil
}

func (r *Resolver) claimSystemAdminOnSignIn(ctx context.Context, user genDb.User, id Identity) error {
	if !id.EmailVerified || !r.systemAdmins.lists(id.Email) {
		return nil
	}
	return pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := r.claimSystemAdmin(ctx, r.queries.WithTx(tx), user, id)
		return err
	})
}

// SystemAdminScope is the user_scopes row that makes userID a system admin.
func SystemAdminScope(userID uuid.UUID) genDb.AddUserScopeParams {
	return genDb.AddUserScopeParams{
		UserID:     userID,
		Scope:      genDb.ScopeAdmin,
		EntityType: genDb.EntityTypeSystem,
		EntityID:   uuid.Nil,
	}
}
