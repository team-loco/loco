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
	errInvalidRoleEmail  = errors.New("not a bare email address")
	errEmailListedTwice  = errors.New("listed as both a system admin and a system reader")
	errRoleClaimsChanged = errors.New("system role claims changed while locking accounts")
)

// SystemRoles lists the configured emails, lowercased, whose first verified sign-in claims each
// installation role.
type SystemRoles struct {
	Admins  []string
	Readers []string
}

// ParseSystemRoles validates the configured admin and reader emails.
func ParseSystemRoles(admins, readers []string) (SystemRoles, error) {
	parsedAdmins, err := parseRoleEmails(admins)
	if err != nil {
		return SystemRoles{}, fmt.Errorf("system admins: %w", err)
	}
	parsedReaders, err := parseRoleEmails(readers)
	if err != nil {
		return SystemRoles{}, fmt.Errorf("system readers: %w", err)
	}
	for _, email := range parsedReaders {
		if slices.Contains(parsedAdmins, email) {
			return SystemRoles{}, fmt.Errorf("%q: %w", email, errEmailListedTwice)
		}
	}
	return SystemRoles{Admins: parsedAdmins, Readers: parsedReaders}, nil
}

func parseRoleEmails(entries []string) ([]string, error) {
	emails := []string{}
	for _, entry := range entries {
		address, err := mail.ParseAddress(entry)
		if err != nil || address.Name != "" || address.Address != entry {
			return nil, fmt.Errorf("%q: %w", entry, errInvalidRoleEmail)
		}
		email := strings.ToLower(entry)
		if !slices.Contains(emails, email) {
			emails = append(emails, email)
		}
	}
	return emails, nil
}

// roleOf returns the role email is configured for.
func (r SystemRoles) roleOf(email string) (genDb.SystemRole, bool) {
	email = strings.ToLower(email)
	switch {
	case email == "":
		return "", false
	case slices.Contains(r.Admins, email):
		return genDb.SystemRoleAdmin, true
	case slices.Contains(r.Readers, email):
		return genDb.SystemRoleRead, true
	}
	return "", false
}

func (r SystemRoles) lists(email string) bool {
	_, ok := r.roleOf(email)
	return ok
}

// SyncSystemRoles makes the claim table match roles: it opens a claim for each new email and role
// and deletes the claims of removed ones, recomputing the role of each user who claimed them.
func SyncSystemRoles(ctx context.Context, pool *pgxpool.Pool, roles SystemRoles) error {
	admins, readers := nonNil(roles.Admins), nonNil(roles.Readers)
	for {
		err := pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
			q := genDb.New(tx)
			entries, err := q.ListSystemRoleEntriesExcept(ctx, genDb.ListSystemRoleEntriesExceptParams{
				Admins: admins, Readers: readers,
			})
			if err != nil {
				return fmt.Errorf("list removed system role entries: %w", err)
			}
			var users []uuid.UUID
			for _, entry := range entries {
				if entry.UserID != nil {
					users = append(users, *entry.UserID)
				}
			}
			if lockErr := authz.LockUsers(ctx, q, users...); errors.Is(lockErr, pgx.ErrNoRows) {
				return errRoleClaimsChanged
			} else if lockErr != nil {
				return fmt.Errorf("lock system role accounts: %w", lockErr)
			}
			locked, err := q.LockSystemRoleEntriesExcept(ctx, genDb.LockSystemRoleEntriesExceptParams{
				Admins: admins, Readers: readers,
			})
			if err != nil {
				return fmt.Errorf("lock removed system role entries: %w", err)
			}
			for _, entry := range locked {
				if entry.UserID != nil && !slices.Contains(users, *entry.UserID) {
					// A claim committed after the account snapshot. Retry before
					// changing anything, preserving user-before-claim lock order.
					return errRoleClaimsChanged
				}
			}
			removed, err := q.RemoveSystemRoleEntriesExcept(ctx, genDb.RemoveSystemRoleEntriesExceptParams{
				Admins: admins, Readers: readers,
			})
			if err != nil {
				return fmt.Errorf("remove system role entries: %w", err)
			}
			for _, entry := range removed {
				if entry.UserID == nil {
					continue
				}
				if err := revokeConfiguredRole(ctx, q, entry); err != nil {
					return err
				}
			}
			if err := q.AddSystemRoleEntries(ctx, genDb.AddSystemRoleEntriesParams{
				Emails: admins, Role: genDb.SystemRoleAdmin,
			}); err != nil {
				return fmt.Errorf("add system admin entries: %w", err)
			}
			if err := q.AddSystemRoleEntries(ctx, genDb.AddSystemRoleEntriesParams{
				Emails: readers, Role: genDb.SystemRoleRead,
			}); err != nil {
				return fmt.Errorf("add system reader entries: %w", err)
			}
			return nil
		})
		if !errors.Is(err, errRoleClaimsChanged) {
			return err
		}
	}
}

func nonNil(emails []string) []string {
	if emails == nil {
		return []string{}
	}
	return emails
}

func revokeConfiguredRole(ctx context.Context, q *genDb.Queries, entry genDb.RemoveSystemRoleEntriesExceptRow) error {
	if _, err := ApplySystemRole(ctx, q, *entry.UserID); err != nil {
		return err
	}
	data := map[string]any{
		events.FieldEmail:  entry.Email,
		events.FieldRole:   entry.Role,
		events.FieldSource: events.SourceConfig,
	}
	if entry.Issuer != nil && entry.Subject != nil {
		data[events.FieldIssuer] = *entry.Issuer
		data[events.FieldSubject] = *entry.Subject
	}
	if err := events.Record(ctx, q, events.Event{
		Type:        events.SystemRoleRevoked,
		ActorType:   events.ActorSystem,
		SubjectType: events.SubjectUser,
		SubjectID:   entry.UserID,
		Data:        data,
	}); err != nil {
		return fmt.Errorf("record system role revoked for user %s: %w", *entry.UserID, err)
	}
	slog.InfoContext(ctx, "revoked system role removed from configuration", "userId", *entry.UserID, "role", entry.Role)
	return nil
}

// ApplySystemRole sets userID's system scope to the higher of its configured and granted roles
// and returns that role, nil when the user holds neither.
func ApplySystemRole(ctx context.Context, q *genDb.Queries, userID uuid.UUID) (*genDb.SystemRole, error) {
	sources, err := q.GetSystemRoleSources(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("look up system roles of user %s: %w", userID, err)
	}
	if err := q.RemoveSystemScopes(ctx, userID); err != nil {
		return nil, fmt.Errorf("remove system scopes of user %s: %w", userID, err)
	}
	role := higherRole(sources.ConfiguredRole, sources.GrantedRole)
	if role == nil {
		return nil, nil
	}
	if err := q.AddUserScope(ctx, systemRoleScope(userID, *role)); err != nil {
		return nil, fmt.Errorf("grant system %s to user %s: %w", *role, userID, err)
	}
	return role, nil
}

func higherRole(a, b *genDb.SystemRole) *genDb.SystemRole {
	if a == nil || IsSystemAdmin(b) {
		return b
	}
	return a
}

// IsSystemAdmin reports whether role is system admin.
func IsSystemAdmin(role *genDb.SystemRole) bool {
	return role != nil && *role == genDb.SystemRoleAdmin
}

// systemRoleScope is the user_scopes row that gives userID role on the installation.
func systemRoleScope(userID uuid.UUID, role genDb.SystemRole) genDb.AddUserScopeParams {
	scope := genDb.ScopeRead
	if role == genDb.SystemRoleAdmin {
		scope = genDb.ScopeAdmin
	}
	return genDb.AddUserScopeParams{
		UserID:     userID,
		Scope:      scope,
		EntityType: genDb.EntityTypeSystem,
		EntityID:   uuid.Nil,
	}
}

// claimSystemRole gives user the configured role of id's verified email when it is an unclaimed entry.
func (r *Resolver) claimSystemRole(
	ctx context.Context,
	qtx *genDb.Queries,
	user genDb.User,
	id Identity,
) (bool, error) {
	role, listed := r.systemRoles.roleOf(id.Email)
	if !id.EmailVerified || !listed || user.Status != genDb.UserStatusActive {
		return false, nil
	}
	var err error
	user, err = lockActiveUser(ctx, qtx, user.ID)
	if err != nil {
		return false, err
	}
	email := strings.ToLower(id.Email)
	claimed, err := qtx.ClaimSystemRole(ctx, genDb.ClaimSystemRoleParams{UserID: user.ID, Email: email})
	if err != nil {
		return false, fmt.Errorf("%w: claim system role for user %s: %w", ErrResolve, user.ID, err)
	}
	if claimed == 0 {
		return false, nil
	}
	if _, err := ApplySystemRole(ctx, qtx, user.ID); err != nil {
		return false, fmt.Errorf("%w: %w", ErrResolve, err)
	}
	if err := events.Record(ctx, qtx, events.Event{
		Type:        events.SystemRoleClaimed,
		ActorType:   string(genDb.EntityTypeUser),
		ActorID:     new(user.ID),
		SubjectType: events.SubjectUser,
		SubjectID:   new(user.ID),
		Data: map[string]any{
			events.FieldEmail:   email,
			events.FieldRole:    role,
			events.FieldIssuer:  id.Issuer,
			events.FieldSubject: id.Subject,
			events.FieldSource:  events.SourceConfig,
		},
	}); err != nil {
		return false, fmt.Errorf("%w: record system role claimed for user %s: %w", ErrResolve, user.ID, err)
	}
	slog.InfoContext(
		ctx,
		"granted system role from configuration",
		"userId",
		user.ID,
		"role",
		role,
		"issuer",
		id.Issuer,
	)
	return true, nil
}

func (r *Resolver) claimSystemRoleOnSignIn(ctx context.Context, user genDb.User, id Identity) error {
	if !id.EmailVerified || !r.systemRoles.lists(id.Email) {
		return nil
	}
	return pgx.BeginFunc(ctx, r.pool, func(tx pgx.Tx) error {
		_, err := r.claimSystemRole(ctx, r.queries.WithTx(tx), user, id)
		return err
	})
}
