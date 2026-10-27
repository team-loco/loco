package auth

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/team-loco/loco/api/auth/authtest"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
)

func open(t *testing.T) SignupPolicy {
	t.Helper()
	p, err := ParseSignupPolicy("open", "")
	if err != nil {
		t.Fatalf("policy: %v", err)
	}
	return p
}

func identity(sub, email string, verified bool) Identity {
	return Identity{
		Issuer:        testIssuerURL,
		Subject:       sub,
		Email:         email,
		EmailVerified: verified,
		Name:          "Dev",
	}
}

func TestResolveProvisionsOnceAndGrantsSelfScopes(t *testing.T) {
	pool := authtest.NewPool(t)
	r := NewResolver(pool, open(t))
	q := genDb.New(pool)

	first, _, err := r.Resolve(t.Context(), identity("s1", "dev@example.test", true))
	if err != nil {
		t.Fatalf("first resolve: %v", err)
	}
	again, _, err := r.Resolve(t.Context(), identity("s1", "dev@example.test", true))
	if err != nil {
		t.Fatalf("second resolve: %v", err)
	}
	if first.ID != again.ID {
		t.Fatalf("second resolve created another user: %s vs %s", first.ID, again.ID)
	}
	if first.Name == nil || *first.Name != "Dev" {
		t.Fatalf("name = %v", first.Name)
	}

	scopes, err := q.GetUserScopes(t.Context(), first.ID)
	if err != nil {
		t.Fatalf("scopes: %v", err)
	}
	if len(scopes) != 3 {
		t.Fatalf("self scopes = %+v", scopes)
	}
}

func TestResolveLinksVerifiedEmailToExistingUser(t *testing.T) {
	pool := authtest.NewPool(t)
	r := NewResolver(pool, open(t))

	github, _, err := r.Resolve(t.Context(), Identity{
		Issuer: "https://github.com", Subject: "42", Email: "dev@example.test", EmailVerified: true,
	})
	if err != nil {
		t.Fatalf("github resolve: %v", err)
	}
	saml, _, err := r.Resolve(t.Context(), identity("saml-user", "dev@example.test", true))
	if err != nil {
		t.Fatalf("saml resolve: %v", err)
	}
	if saml.ID != github.ID {
		t.Fatalf("verified email created a second user: %s vs %s", saml.ID, github.ID)
	}
}

func TestResolveRejectsUnverifiedEmailCollision(t *testing.T) {
	pool := authtest.NewPool(t)
	r := NewResolver(pool, open(t))
	q := genDb.New(pool)

	if _, _, err := r.Resolve(t.Context(), identity("owner", "dev@example.test", true)); err != nil {
		t.Fatalf("owner resolve: %v", err)
	}
	_, _, err := r.Resolve(t.Context(), identity("intruder", "dev@example.test", false))
	if !errors.Is(err, ErrEmailUnverified) {
		t.Fatalf("unverified collision err = %v, want %v", err, ErrEmailUnverified)
	}
	_, err = q.GetUserByIdentity(t.Context(), genDb.GetUserByIdentityParams{
		Issuer: testIssuerURL, Subject: "intruder",
	})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("intruder identity stored: %v", err)
	}
}

func TestResolveUnverifiedIdentityNeverCreatesAnAccount(t *testing.T) {
	pool := authtest.NewPool(t)
	r := NewResolver(pool, open(t))
	q := genDb.New(pool)

	_, _, err := r.Resolve(t.Context(), identity("squatter", "dev@example.test", false))
	if !errors.Is(err, ErrEmailUnverified) {
		t.Fatalf("unverified signup err = %v, want %v", err, ErrEmailUnverified)
	}
	if _, lookupErr := q.GetUserByEmail(t.Context(), "dev@example.test"); !errors.Is(lookupErr, pgx.ErrNoRows) {
		t.Fatalf("unverified identity created a user: %v", lookupErr)
	}

	if _, _, ownerErr := r.Resolve(t.Context(), identity("owner", "dev@example.test", true)); ownerErr != nil {
		t.Fatalf("owner resolve: %v", ownerErr)
	}
	_, _, err = r.Resolve(t.Context(), identity("squatter", "dev@example.test", false))
	if !errors.Is(err, ErrEmailUnverified) {
		t.Fatalf("squatter after owner err = %v, want %v", err, ErrEmailUnverified)
	}
	_, err = q.GetUserByIdentity(t.Context(), genDb.GetUserByIdentityParams{
		Issuer: testIssuerURL, Subject: "squatter",
	})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("squatter identity stored: %v", err)
	}
}

func TestResolveDoesNotLinkIntoAccountWithUnverifiedIdentity(t *testing.T) {
	pool := authtest.NewPool(t)
	r := NewResolver(pool, open(t))
	q := genDb.New(pool)

	if _, _, err := r.Resolve(t.Context(), identity("first", "dev@example.test", true)); err != nil {
		t.Fatalf("first resolve: %v", err)
	}
	if _, _, err := r.Resolve(t.Context(), identity("first", "dev@example.test", false)); err != nil {
		t.Fatalf("first identity, now unverified: %v", err)
	}

	_, _, err := r.Resolve(t.Context(), identity("second", "dev@example.test", true))
	if !errors.Is(err, ErrEmailTaken) {
		t.Fatalf("link into unverified account err = %v, want %v", err, ErrEmailTaken)
	}
	_, err = q.GetUserByIdentity(t.Context(), genDb.GetUserByIdentityParams{
		Issuer: testIssuerURL, Subject: "second",
	})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("second identity stored: %v", err)
	}
}

func TestResolvePolicyRejectionCreatesNothing(t *testing.T) {
	pool := authtest.NewPool(t)
	policy, err := ParseSignupPolicy("domains", "acme.test")
	if err != nil {
		t.Fatalf("policy: %v", err)
	}
	r := NewResolver(pool, policy)
	q := genDb.New(pool)

	_, _, err = r.Resolve(t.Context(), identity("s1", "dev@evil.test", true))
	if rejected, ok := errors.AsType[*SignupRejectedError](err); !ok {
		t.Fatalf("err = %v, want rejection", err)
	} else if rejected.Message == "" {
		t.Fatal("rejection has no message")
	}
	if _, err := q.GetUserByEmail(t.Context(), "dev@evil.test"); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("rejected user stored: %v", err)
	}

	if _, _, err := r.Resolve(t.Context(), identity("s2", "dev@acme.test", true)); err != nil {
		t.Fatalf("allowed domain: %v", err)
	}
}

func TestResolvePolicyDoesNotBlockExistingUsers(t *testing.T) {
	pool := authtest.NewPool(t)
	openResolver := NewResolver(pool, open(t))
	if _, _, err := openResolver.Resolve(t.Context(), identity("s1", "dev@example.test", true)); err != nil {
		t.Fatalf("seed: %v", err)
	}
	invite, err := ParseSignupPolicy("invite", "")
	if err != nil {
		t.Fatalf("policy: %v", err)
	}
	r := NewResolver(pool, invite)
	if _, _, err := r.Resolve(t.Context(), identity("s1", "dev@example.test", true)); err != nil {
		t.Fatalf("existing identity blocked: %v", err)
	}
	if _, _, err := r.Resolve(t.Context(), identity("s2", "dev@example.test", true)); err != nil {
		t.Fatalf("verified link to existing user blocked: %v", err)
	}
}

func TestResolveRequiresEmail(t *testing.T) {
	pool := authtest.NewPool(t)
	r := NewResolver(pool, open(t))
	_, _, err := r.Resolve(t.Context(), identity("s1", "", false))
	if rejected, ok := errors.AsType[*SignupRejectedError](err); !ok {
		t.Fatalf("err = %v, want rejection", err)
	} else if rejected.Message == "" {
		t.Fatal("rejection has no message")
	}
}

func TestResolveConcurrentFirstLoginsCreateOneUser(t *testing.T) {
	pool := authtest.NewPool(t)
	r := NewResolver(pool, open(t))

	const n = 8
	ids := make([]uuid.UUID, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			user, _, err := r.Resolve(context.Background(), identity("race", "race@example.test", true))
			ids[i], errs[i] = user.ID, err
		})
	}
	wg.Wait()
	for i := range n {
		if errs[i] != nil {
			t.Fatalf("resolve %d: %v", i, errs[i])
		}
		if ids[i] != ids[0] {
			t.Fatalf("resolve %d got user %s, want %s", i, ids[i], ids[0])
		}
	}
}

const (
	oldAccountEmail = "old@example.test"
	newAccountEmail = "new@example.test"
)

func TestResolveVerifiedEmailChangeMovesTheAccountEmail(t *testing.T) {
	pool := authtest.NewPool(t)
	r := NewResolver(pool, open(t))
	q := genDb.New(pool)

	user, _, err := r.Resolve(t.Context(), identity("mover", oldAccountEmail, true))
	if err != nil {
		t.Fatalf("signup: %v", err)
	}
	moved, _, err := r.Resolve(t.Context(), identity("mover", newAccountEmail, true))
	if err != nil {
		t.Fatalf("sign-in with a changed email: %v", err)
	}
	if moved.ID != user.ID || moved.Email != newAccountEmail {
		t.Fatalf("resolved user = %s %s, want %s %s", moved.ID, moved.Email, user.ID, newAccountEmail)
	}
	stored, err := q.GetUserByID(t.Context(), user.ID)
	if err != nil || stored.Email != newAccountEmail {
		t.Fatalf("account email = %q %v, want %s", stored.Email, err, newAccountEmail)
	}

	other, _, err := r.Resolve(t.Context(), Identity{
		Issuer: "https://other.example.test", Subject: "elsewhere", Email: newAccountEmail, EmailVerified: true,
	})
	if err != nil {
		t.Fatalf("other issuer at the new address: %v", err)
	}
	if other.ID != user.ID {
		t.Fatalf("other issuer at the new address created user %s, want %s", other.ID, user.ID)
	}
}

func TestResolveUnverifiedEmailChangeKeepsTheAccountEmail(t *testing.T) {
	pool := authtest.NewPool(t)
	r := NewResolver(pool, open(t))
	q := genDb.New(pool)

	user, _, err := r.Resolve(t.Context(), identity("mover", oldAccountEmail, true))
	if err != nil {
		t.Fatalf("signup: %v", err)
	}
	resolved, _, err := r.Resolve(t.Context(), identity("mover", "unconfirmed@example.test", false))
	if err != nil {
		t.Fatalf("sign-in with an unverified email: %v", err)
	}
	if resolved.Email != oldAccountEmail {
		t.Fatalf("resolved email = %q, want %s", resolved.Email, oldAccountEmail)
	}
	stored, err := q.GetUserByID(t.Context(), user.ID)
	if err != nil || stored.Email != oldAccountEmail {
		t.Fatalf("account email = %q %v, want %s", stored.Email, err, oldAccountEmail)
	}

	confirmed, _, err := r.Resolve(t.Context(), identity("mover", "unconfirmed@example.test", true))
	if err != nil {
		t.Fatalf("sign-in once the email is verified: %v", err)
	}
	if confirmed.Email != "unconfirmed@example.test" {
		t.Fatalf("verified email did not move the account email: %q", confirmed.Email)
	}
}

func TestResolveVerifiedEmailChangeKeepsAnAddressAnotherUserHas(t *testing.T) {
	pool := authtest.NewPool(t)
	r := NewResolver(pool, open(t))
	q := genDb.New(pool)

	mover, _, err := r.Resolve(t.Context(), identity("mover", oldAccountEmail, true))
	if err != nil {
		t.Fatalf("signup: %v", err)
	}
	holder, _, err := r.Resolve(t.Context(), identity("holder", "taken@example.test", true))
	if err != nil {
		t.Fatalf("second signup: %v", err)
	}
	resolved, _, err := r.Resolve(t.Context(), identity("mover", "taken@example.test", true))
	if err != nil {
		t.Fatalf("sign-in with another user's address: %v", err)
	}
	if resolved.ID != mover.ID || resolved.Email != oldAccountEmail {
		t.Fatalf("resolved user = %s %s, want %s %s", resolved.ID, resolved.Email, mover.ID, oldAccountEmail)
	}
	stored, err := q.GetUserByID(t.Context(), mover.ID)
	if err != nil || stored.Email != oldAccountEmail {
		t.Fatalf("account email = %q %v, want %s", stored.Email, err, oldAccountEmail)
	}
	kept, err := q.GetUserByID(t.Context(), holder.ID)
	if err != nil || kept.Email != "taken@example.test" {
		t.Fatalf("holder email = %q %v, want taken@example.test", kept.Email, err)
	}
}

func TestResolveEmailChangeKeepsAnAccountEmailAnotherIdentityConfirms(t *testing.T) {
	pool := authtest.NewPool(t)
	r := NewResolver(pool, open(t))

	user, _, err := r.Resolve(t.Context(), identity("first", "shared@example.test", true))
	if err != nil {
		t.Fatalf("signup: %v", err)
	}
	if _, _, linkErr := r.Resolve(t.Context(), identity("second", "shared@example.test", true)); linkErr != nil {
		t.Fatalf("link: %v", linkErr)
	}
	resolved, _, err := r.Resolve(t.Context(), identity("second", "moved@example.test", true))
	if err != nil {
		t.Fatalf("sign-in with a changed email: %v", err)
	}
	if resolved.ID != user.ID || resolved.Email != "shared@example.test" {
		t.Fatalf("resolved user = %s %s, want %s shared@example.test", resolved.ID, resolved.Email, user.ID)
	}
}

type fakeEmailVerifier struct {
	mu       sync.Mutex
	verified map[string]bool
	calls    int
}

func (f *fakeEmailVerifier) EmailVerified(_ context.Context, subject, _ string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.verified[subject], nil
}

func (f *fakeEmailVerifier) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func TestResolveAsksTheProviderOnlyWhenItNeedsTheVerifiedState(t *testing.T) {
	pool := authtest.NewPool(t)
	lookup := &fakeEmailVerifier{verified: map[string]bool{subjectConfirmed: true}}
	r := NewResolver(pool, open(t), WithEmailVerifiers(EmailVerifiers{testIssuerURL: lookup}))
	q := genDb.New(pool)

	_, _, err := r.Resolve(t.Context(), identity("self-asserted", "squat@example.test", true))
	if !errors.Is(err, ErrEmailUnverified) {
		t.Fatalf("unconfirmed signup err = %v, want %v", err, ErrEmailUnverified)
	}
	if lookup.callCount() != 1 {
		t.Fatalf("provider lookups = %d, want 1", lookup.callCount())
	}

	user, _, err := r.Resolve(t.Context(), identity(subjectConfirmed, "returning@example.test", false))
	if err != nil {
		t.Fatalf("confirmed signup: %v", err)
	}
	stored, err := q.GetIdentity(t.Context(), genDb.GetIdentityParams{Issuer: testIssuerURL, Subject: subjectConfirmed})
	if err != nil || !stored.EmailVerified || stored.UserID != user.ID {
		t.Fatalf("stored identity = %+v %v", stored, err)
	}
	if lookup.callCount() != 2 {
		t.Fatalf("provider lookups = %d, want 2", lookup.callCount())
	}

	returning := identity(subjectConfirmed, "returning@example.test", false)
	if _, _, returnErr := r.Resolve(t.Context(), returning); returnErr != nil {
		t.Fatalf("returning sign-in: %v", returnErr)
	}
	if lookup.callCount() != 2 {
		t.Fatalf("returning sign-in with a verified identity asked the provider: %d lookups", lookup.callCount())
	}
	stored, err = q.GetIdentity(t.Context(), genDb.GetIdentityParams{Issuer: testIssuerURL, Subject: subjectConfirmed})
	if err != nil || !stored.EmailVerified {
		t.Fatalf("returning sign-in lost the verified state: %+v %v", stored, err)
	}

	lookup.verified[subjectConfirmed] = false
	changed := identity(subjectConfirmed, "changed@example.test", false)
	if _, _, changeErr := r.Resolve(t.Context(), changed); changeErr != nil {
		t.Fatalf("sign-in with a changed email: %v", changeErr)
	}
	if lookup.callCount() != 3 {
		t.Fatalf("changed email did not ask the provider: %d lookups", lookup.callCount())
	}
	stored, err = q.GetIdentity(t.Context(), genDb.GetIdentityParams{Issuer: testIssuerURL, Subject: subjectConfirmed})
	if err != nil || stored.EmailVerified {
		t.Fatalf("unconfirmed new email stored as verified: %+v %v", stored, err)
	}
}

func TestResolveProviderConfirmedEmailChangeMovesTheAccountEmail(t *testing.T) {
	pool := authtest.NewPool(t)
	lookup := &fakeEmailVerifier{verified: map[string]bool{subjectConfirmed: true}}
	r := NewResolver(pool, open(t), WithEmailVerifiers(EmailVerifiers{testIssuerURL: lookup}))

	user, _, err := r.Resolve(t.Context(), identity(subjectConfirmed, oldAccountEmail, false))
	if err != nil {
		t.Fatalf("signup: %v", err)
	}
	lookup.verified[subjectConfirmed] = false
	pending, _, err := r.Resolve(t.Context(), identity(subjectConfirmed, newAccountEmail, false))
	if err != nil {
		t.Fatalf("sign-in with an unconfirmed email: %v", err)
	}
	if pending.Email != oldAccountEmail {
		t.Fatalf("unconfirmed email moved the account email to %q", pending.Email)
	}
	lookup.verified[subjectConfirmed] = true
	confirmed, _, err := r.Resolve(t.Context(), identity(subjectConfirmed, newAccountEmail, false))
	if err != nil {
		t.Fatalf("sign-in once the provider confirms the email: %v", err)
	}
	if confirmed.ID != user.ID || confirmed.Email != newAccountEmail {
		t.Fatalf("resolved user = %s %s, want %s %s", confirmed.ID, confirmed.Email, user.ID, newAccountEmail)
	}
}

func TestResolveRecordsTheAccountEmailMove(t *testing.T) {
	pool := authtest.NewPool(t)
	r := NewResolver(pool, open(t))

	user, _, err := r.Resolve(t.Context(), identity("mover", oldAccountEmail, true))
	if err != nil {
		t.Fatalf("signup: %v", err)
	}
	if _, _, moveErr := r.Resolve(t.Context(), identity("mover", newAccountEmail, true)); moveErr != nil {
		t.Fatalf("sign-in with a changed email: %v", moveErr)
	}
	var recorded int
	if err := pool.QueryRow(t.Context(),
		`SELECT COUNT(*) FROM events WHERE type = $1 AND subject_id = $2 AND data->>'email' = $3`,
		events.UserUpdated, user.ID, newAccountEmail).Scan(&recorded); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if recorded != 1 {
		t.Fatalf("account email change events = %d, want 1", recorded)
	}
}

const (
	invitationTTL  = time.Hour
	inviteeEmail   = "bob@x.test"
	inviteeSubject = "bob"
)

type invitationFixture struct {
	pool    *pgxpool.Pool
	q       *genDb.Queries
	inviter genDb.User
}

func newInvitationFixture(t *testing.T) *invitationFixture {
	t.Helper()
	pool := authtest.NewPool(t)
	q := genDb.New(pool)
	inviter, err := q.CreateUser(t.Context(), genDb.CreateUserParams{Email: "admin@x.test"})
	if err != nil {
		t.Fatalf("create inviter: %v", err)
	}
	return &invitationFixture{pool: pool, q: q, inviter: inviter}
}

func (f *invitationFixture) resolver(t *testing.T, mode string) *Resolver {
	t.Helper()
	policy, err := ParseSignupPolicy(mode, "")
	if err != nil {
		t.Fatalf("policy: %v", err)
	}
	return NewResolver(f.pool, policy)
}

func (f *invitationFixture) org(t *testing.T, name string) uuid.UUID {
	t.Helper()
	org, err := f.q.CreateOrg(t.Context(), genDb.CreateOrgParams{Name: name, CreatedBy: f.inviter.ID})
	if err != nil {
		t.Fatalf("create org %s: %v", name, err)
	}
	return org.ID
}

func (f *invitationFixture) invite(t *testing.T, orgID uuid.UUID, email string, ttl time.Duration) genDb.Invitation {
	t.Helper()
	invitation, err := f.q.CreateInvitation(t.Context(), genDb.CreateInvitationParams{
		OrgID:     orgID,
		Email:     email,
		InvitedBy: f.inviter.ID,
		ExpiresAt: time.Now().Add(ttl),
	})
	if err != nil {
		t.Fatalf("create invitation: %v", err)
	}
	return invitation
}

func (f *invitationFixture) isMember(t *testing.T, userID, orgID uuid.UUID) bool {
	t.Helper()
	member, err := f.q.IsOrgMember(t.Context(), genDb.IsOrgMemberParams{UserID: userID, OrgID: orgID})
	if err != nil {
		t.Fatalf("is member: %v", err)
	}
	return member
}

func (f *invitationFixture) claimedBy(t *testing.T, id uuid.UUID) *uuid.UUID {
	t.Helper()
	invitation, err := f.q.GetInvitation(t.Context(), id)
	if err != nil {
		t.Fatalf("get invitation: %v", err)
	}
	return invitation.ClaimedBy
}

func (f *invitationFixture) events(t *testing.T, eventType string, subjectID uuid.UUID) int {
	t.Helper()
	var n int
	if err := f.pool.QueryRow(t.Context(),
		`SELECT COUNT(*) FROM events WHERE type = $1 AND subject_id = $2`, eventType, subjectID).Scan(&n); err != nil {
		t.Fatalf("count events: %v", err)
	}
	return n
}

func TestResolveInviteModeClaimsTheInvitationForANewUser(t *testing.T) {
	f := newInvitationFixture(t)
	orgID := f.org(t, "acme")
	invitation := f.invite(t, orgID, inviteeEmail, invitationTTL)

	user, claimed, err := f.resolver(t, "invite").Resolve(t.Context(), identity(inviteeSubject, inviteeEmail, true))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !f.isMember(t, user.ID, orgID) {
		t.Fatal("invited user is not an org member")
	}
	wantClaimed := []ClaimedOrg{{OrgID: orgID, OrgName: "acme", InviterName: f.inviter.Email}}
	if !slices.Equal(claimed, wantClaimed) {
		t.Fatalf("claimed orgs = %+v, want %+v", claimed, wantClaimed)
	}
	if claimed := f.claimedBy(t, invitation.ID); claimed == nil || *claimed != user.ID {
		t.Fatalf("claimed_by = %v, want %s", claimed, user.ID)
	}
	if n := f.events(t, events.OrgMemberAdded, user.ID); n != 1 {
		t.Fatalf("org_member.added events = %d, want 1", n)
	}
	if n := f.events(t, events.InvitationClaimed, invitation.ID); n != 1 {
		t.Fatalf("invitation.claimed events = %d, want 1", n)
	}
}

func TestResolveInvitationClaimRollsBackWithTheSignup(t *testing.T) {
	f := newInvitationFixture(t)
	orgID := f.org(t, "acme")
	invitation := f.invite(t, orgID, inviteeEmail, invitationTTL)
	if _, err := f.pool.Exec(t.Context(), `
		CREATE FUNCTION fail_claim() RETURNS trigger AS $$
		BEGIN RAISE EXCEPTION 'injected failure'; END;
		$$ LANGUAGE plpgsql;
		CREATE TRIGGER fail_claim BEFORE INSERT ON events
		FOR EACH ROW WHEN (NEW.type = 'invitation.claimed') EXECUTE FUNCTION fail_claim();`); err != nil {
		t.Fatalf("install failure trigger: %v", err)
	}

	_, _, err := f.resolver(t, "invite").Resolve(t.Context(), identity(inviteeSubject, inviteeEmail, true))
	if !errors.Is(err, ErrResolve) {
		t.Fatalf("resolve err = %v, want %v", err, ErrResolve)
	}
	if _, lookupErr := f.q.GetUserByEmail(t.Context(), inviteeEmail); !errors.Is(lookupErr, pgx.ErrNoRows) {
		t.Fatalf("user persisted after a failed claim: %v", lookupErr)
	}
	if claimed := f.claimedBy(t, invitation.ID); claimed != nil {
		t.Fatalf("invitation claimed by %s after a failed claim", claimed)
	}
	var scopes, memberEvents int
	if err := f.pool.QueryRow(t.Context(),
		`SELECT COUNT(*) FROM user_scopes WHERE entity_id = $1`, orgID).Scan(&scopes); err != nil {
		t.Fatalf("count scopes: %v", err)
	}
	if err := f.pool.QueryRow(t.Context(),
		`SELECT COUNT(*) FROM events WHERE type = ANY($1)`,
		[]string{events.UserCreated, events.OrgMemberAdded}).Scan(&memberEvents); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if scopes != 0 || memberEvents != 0 {
		t.Fatalf("failed claim left %d org scopes and %d events", scopes, memberEvents)
	}
}

func TestResolveWithoutInvitation(t *testing.T) {
	f := newInvitationFixture(t)

	_, _, err := f.resolver(t, "invite").Resolve(t.Context(), identity(inviteeSubject, inviteeEmail, true))
	if rejected, ok := errors.AsType[*SignupRejectedError](err); !ok || rejected.Message == "" {
		t.Fatalf("invite mode err = %v, want rejection", err)
	}
	if _, lookupErr := f.q.GetUserByEmail(t.Context(), inviteeEmail); !errors.Is(lookupErr, pgx.ErrNoRows) {
		t.Fatalf("rejected user stored: %v", lookupErr)
	}

	user, _, err := f.resolver(t, "open").Resolve(t.Context(), identity(inviteeSubject, inviteeEmail, true))
	if err != nil {
		t.Fatalf("open mode: %v", err)
	}
	scopes, err := f.q.GetUserScopes(t.Context(), user.ID)
	if err != nil {
		t.Fatalf("scopes: %v", err)
	}
	for _, scope := range scopes {
		if scope.EntityType != genDb.EntityTypeUser {
			t.Fatalf("uninvited user got scope %+v", scope)
		}
	}
}

func TestResolveClaimsAnInvitationOnTheNextRequest(t *testing.T) {
	f := newInvitationFixture(t)
	r := f.resolver(t, "open")
	user, _, err := r.Resolve(t.Context(), identity(inviteeSubject, inviteeEmail, true))
	if err != nil {
		t.Fatalf("signup: %v", err)
	}
	orgID := f.org(t, "acme")
	invitation := f.invite(t, orgID, inviteeEmail, invitationTTL)

	_, claimed, err := r.Resolve(t.Context(), identity(inviteeSubject, inviteeEmail, true))
	if err != nil {
		t.Fatalf("next request: %v", err)
	}
	want := []ClaimedOrg{{OrgID: orgID, OrgName: "acme", InviterName: f.inviter.Email}}
	if !slices.Equal(claimed, want) {
		t.Fatalf("claimed orgs = %+v, want %+v", claimed, want)
	}
	_, again, err := r.Resolve(t.Context(), identity(inviteeSubject, inviteeEmail, true))
	if err != nil || len(again) != 0 {
		t.Fatalf("claimed orgs on the following request = %+v %v, want none", again, err)
	}
	if !f.isMember(t, user.ID, orgID) {
		t.Fatal("existing user is not a member after the next request")
	}
	if claimed := f.claimedBy(t, invitation.ID); claimed == nil || *claimed != user.ID {
		t.Fatalf("claimed_by = %v, want %s", claimed, user.ID)
	}
	if n := f.events(t, events.InvitationClaimed, invitation.ID); n != 1 {
		t.Fatalf("invitation.claimed events = %d, want 1", n)
	}
}

func TestResolveClaimsOnlyOpenMatchingInvitations(t *testing.T) {
	f := newInvitationFixture(t)
	expired := f.org(t, "expired")
	revoked := f.org(t, "revoked")
	taken := f.org(t, "taken")
	other := f.org(t, "other")
	matching := f.org(t, "matching")
	f.invite(t, expired, inviteeEmail, -time.Minute)
	if _, err := f.q.RevokeInvitation(t.Context(), f.invite(t, revoked, inviteeEmail, invitationTTL).ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := f.q.ClaimInvitation(t.Context(), genDb.ClaimInvitationParams{
		ID:     f.invite(t, taken, inviteeEmail, invitationTTL).ID,
		UserID: f.inviter.ID,
	}); err != nil {
		t.Fatalf("claim: %v", err)
	}
	f.invite(t, other, "carol@x.test", invitationTTL)
	f.invite(t, matching, "Bob@X.test", invitationTTL)

	user, _, err := f.resolver(t, "open").Resolve(t.Context(), identity(inviteeSubject, inviteeEmail, true))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	for name, orgID := range map[string]uuid.UUID{
		"expired": expired, "revoked": revoked, "taken": taken, "other": other,
	} {
		if f.isMember(t, user.ID, orgID) {
			t.Errorf("%s invitation was claimed", name)
		}
	}
	if !f.isMember(t, user.ID, matching) {
		t.Error("invitation for Bob@X.test was not claimed by bob@x.test")
	}
}

func TestResolveClaimsInvitationsFromSeveralOrgsTogether(t *testing.T) {
	f := newInvitationFixture(t)
	first := f.org(t, "first")
	second := f.org(t, "second")
	f.invite(t, first, inviteeEmail, invitationTTL)
	f.invite(t, second, inviteeEmail, invitationTTL)

	user, _, err := f.resolver(t, "invite").Resolve(t.Context(), identity(inviteeSubject, inviteeEmail, true))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !f.isMember(t, user.ID, first) || !f.isMember(t, user.ID, second) {
		t.Fatal("not a member of both inviting orgs")
	}
	if n := f.events(t, events.OrgMemberAdded, user.ID); n != 2 {
		t.Fatalf("org_member.added events = %d, want 2", n)
	}
}

func TestResolveUnverifiedEmailNeverClaims(t *testing.T) {
	f := newInvitationFixture(t)
	r := f.resolver(t, "open")
	user, _, err := r.Resolve(t.Context(), identity(inviteeSubject, inviteeEmail, true))
	if err != nil {
		t.Fatalf("signup: %v", err)
	}
	orgID := f.org(t, "acme")
	invitation := f.invite(t, orgID, inviteeEmail, invitationTTL)

	unverified := identity(inviteeSubject, inviteeEmail, false)
	if _, _, unverifiedErr := r.Resolve(t.Context(), unverified); unverifiedErr != nil {
		t.Fatalf("unverified sign-in: %v", unverifiedErr)
	}
	if f.isMember(t, user.ID, orgID) || f.claimedBy(t, invitation.ID) != nil {
		t.Fatal("unverified email claimed the invitation")
	}

	carol := f.invite(t, orgID, "carol@x.test", invitationTTL)
	_, _, err = r.Resolve(t.Context(), Identity{
		Issuer: "https://other.example.test", Subject: "squatter", Email: "carol@x.test", EmailVerified: false,
	})
	if !errors.Is(err, ErrEmailUnverified) {
		t.Fatalf("unverified new identity err = %v, want %v", err, ErrEmailUnverified)
	}
	if f.claimedBy(t, carol.ID) != nil {
		t.Fatal("unverified new identity claimed an invitation")
	}
}

func TestResolveSuspendedUserDoesNotClaim(t *testing.T) {
	f := newInvitationFixture(t)
	r := f.resolver(t, "open")
	user, _, err := r.Resolve(t.Context(), identity(inviteeSubject, inviteeEmail, true))
	if err != nil {
		t.Fatalf("signup: %v", err)
	}
	if _, err := f.q.SetUserStatus(t.Context(), genDb.SetUserStatusParams{
		ID: user.ID, Status: genDb.UserStatusSuspended,
	}); err != nil {
		t.Fatalf("suspend: %v", err)
	}
	orgID := f.org(t, "acme")
	invitation := f.invite(t, orgID, inviteeEmail, invitationTTL)

	if _, _, err := r.Resolve(t.Context(), identity(inviteeSubject, inviteeEmail, true)); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if f.isMember(t, user.ID, orgID) || f.claimedBy(t, invitation.ID) != nil {
		t.Fatal("suspended user claimed an invitation")
	}
}

func TestResolveRemovedMemberIsNotReAdded(t *testing.T) {
	f := newInvitationFixture(t)
	r := f.resolver(t, "invite")
	orgID := f.org(t, "acme")
	invitation := f.invite(t, orgID, inviteeEmail, invitationTTL)
	user, _, err := r.Resolve(t.Context(), identity(inviteeSubject, inviteeEmail, true))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if err := f.q.RemoveOrgMember(t.Context(), genDb.RemoveOrgMemberParams{UserID: user.ID, OrgID: orgID}); err != nil {
		t.Fatalf("remove member: %v", err)
	}

	if _, _, err := r.Resolve(t.Context(), identity(inviteeSubject, inviteeEmail, true)); err != nil {
		t.Fatalf("next request: %v", err)
	}
	if f.isMember(t, user.ID, orgID) {
		t.Fatal("removed member was re-added")
	}
	if claimed := f.claimedBy(t, invitation.ID); claimed == nil || *claimed != user.ID {
		t.Fatalf("claimed_by = %v, want %s", claimed, user.ID)
	}
}

func TestDuplicateOpenInvitationIsRejected(t *testing.T) {
	f := newInvitationFixture(t)
	orgID := f.org(t, "acme")
	first := f.invite(t, orgID, inviteeEmail, invitationTTL)

	_, err := f.q.CreateInvitation(t.Context(), genDb.CreateInvitationParams{
		OrgID: orgID, Email: "BOB@x.test", InvitedBy: f.inviter.ID, ExpiresAt: time.Now().Add(invitationTTL),
	})
	if !isUniqueViolation(err) {
		t.Fatalf("duplicate open invitation err = %v, want a unique violation", err)
	}

	if _, err := f.q.RevokeInvitation(t.Context(), first.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	f.invite(t, orgID, inviteeEmail, invitationTTL)
	f.invite(t, f.org(t, "other"), inviteeEmail, invitationTTL)
}
