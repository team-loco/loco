package auth

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
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

	first, err := r.Resolve(t.Context(), identity("s1", "dev@example.test", true))
	if err != nil {
		t.Fatalf("first resolve: %v", err)
	}
	again, err := r.Resolve(t.Context(), identity("s1", "dev@example.test", true))
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

	github, err := r.Resolve(t.Context(), Identity{
		Issuer: "https://github.com", Subject: "42", Email: "dev@example.test", EmailVerified: true,
	})
	if err != nil {
		t.Fatalf("github resolve: %v", err)
	}
	saml, err := r.Resolve(t.Context(), identity("saml-user", "dev@example.test", true))
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

	if _, err := r.Resolve(t.Context(), identity("owner", "dev@example.test", true)); err != nil {
		t.Fatalf("owner resolve: %v", err)
	}
	_, err := r.Resolve(t.Context(), identity("intruder", "dev@example.test", false))
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

	_, err := r.Resolve(t.Context(), identity("squatter", "dev@example.test", false))
	if !errors.Is(err, ErrEmailUnverified) {
		t.Fatalf("unverified signup err = %v, want %v", err, ErrEmailUnverified)
	}
	if _, lookupErr := q.GetUserByEmail(t.Context(), "dev@example.test"); !errors.Is(lookupErr, pgx.ErrNoRows) {
		t.Fatalf("unverified identity created a user: %v", lookupErr)
	}

	if _, ownerErr := r.Resolve(t.Context(), identity("owner", "dev@example.test", true)); ownerErr != nil {
		t.Fatalf("owner resolve: %v", ownerErr)
	}
	_, err = r.Resolve(t.Context(), identity("squatter", "dev@example.test", false))
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

	if _, err := r.Resolve(t.Context(), identity("first", "dev@example.test", true)); err != nil {
		t.Fatalf("first resolve: %v", err)
	}
	if _, err := r.Resolve(t.Context(), identity("first", "dev@example.test", false)); err != nil {
		t.Fatalf("first identity, now unverified: %v", err)
	}

	_, err := r.Resolve(t.Context(), identity("second", "dev@example.test", true))
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

	_, err = r.Resolve(t.Context(), identity("s1", "dev@evil.test", true))
	if rejected, ok := errors.AsType[*SignupRejectedError](err); !ok {
		t.Fatalf("err = %v, want rejection", err)
	} else if rejected.Message == "" {
		t.Fatal("rejection has no message")
	}
	if _, err := q.GetUserByEmail(t.Context(), "dev@evil.test"); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("rejected user stored: %v", err)
	}

	if _, err := r.Resolve(t.Context(), identity("s2", "dev@acme.test", true)); err != nil {
		t.Fatalf("allowed domain: %v", err)
	}
}

func TestResolvePolicyDoesNotBlockExistingUsers(t *testing.T) {
	pool := authtest.NewPool(t)
	openResolver := NewResolver(pool, open(t))
	if _, err := openResolver.Resolve(t.Context(), identity("s1", "dev@example.test", true)); err != nil {
		t.Fatalf("seed: %v", err)
	}
	closed, err := ParseSignupPolicy("closed", "")
	if err != nil {
		t.Fatalf("policy: %v", err)
	}
	r := NewResolver(pool, closed)
	if _, err := r.Resolve(t.Context(), identity("s1", "dev@example.test", true)); err != nil {
		t.Fatalf("existing identity blocked: %v", err)
	}
	if _, err := r.Resolve(t.Context(), identity("s2", "dev@example.test", true)); err != nil {
		t.Fatalf("verified link to existing user blocked: %v", err)
	}
}

func TestResolveRequiresEmail(t *testing.T) {
	pool := authtest.NewPool(t)
	r := NewResolver(pool, open(t))
	_, err := r.Resolve(t.Context(), identity("s1", "", false))
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
			user, err := r.Resolve(context.Background(), identity("race", "race@example.test", true))
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

	user, err := r.Resolve(t.Context(), identity("mover", oldAccountEmail, true))
	if err != nil {
		t.Fatalf("signup: %v", err)
	}
	moved, err := r.Resolve(t.Context(), identity("mover", newAccountEmail, true))
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

	other, err := r.Resolve(t.Context(), Identity{
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

	user, err := r.Resolve(t.Context(), identity("mover", oldAccountEmail, true))
	if err != nil {
		t.Fatalf("signup: %v", err)
	}
	resolved, err := r.Resolve(t.Context(), identity("mover", "unconfirmed@example.test", false))
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

	confirmed, err := r.Resolve(t.Context(), identity("mover", "unconfirmed@example.test", true))
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

	mover, err := r.Resolve(t.Context(), identity("mover", oldAccountEmail, true))
	if err != nil {
		t.Fatalf("signup: %v", err)
	}
	holder, err := r.Resolve(t.Context(), identity("holder", "taken@example.test", true))
	if err != nil {
		t.Fatalf("second signup: %v", err)
	}
	resolved, err := r.Resolve(t.Context(), identity("mover", "taken@example.test", true))
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

	user, err := r.Resolve(t.Context(), identity("first", "shared@example.test", true))
	if err != nil {
		t.Fatalf("signup: %v", err)
	}
	if _, linkErr := r.Resolve(t.Context(), identity("second", "shared@example.test", true)); linkErr != nil {
		t.Fatalf("link: %v", linkErr)
	}
	resolved, err := r.Resolve(t.Context(), identity("second", "moved@example.test", true))
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

	_, err := r.Resolve(t.Context(), identity("self-asserted", "squat@example.test", true))
	if !errors.Is(err, ErrEmailUnverified) {
		t.Fatalf("unconfirmed signup err = %v, want %v", err, ErrEmailUnverified)
	}
	if lookup.callCount() != 1 {
		t.Fatalf("provider lookups = %d, want 1", lookup.callCount())
	}

	user, err := r.Resolve(t.Context(), identity(subjectConfirmed, "returning@example.test", false))
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
	if _, returnErr := r.Resolve(t.Context(), returning); returnErr != nil {
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
	if _, changeErr := r.Resolve(t.Context(), changed); changeErr != nil {
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

	user, err := r.Resolve(t.Context(), identity(subjectConfirmed, oldAccountEmail, false))
	if err != nil {
		t.Fatalf("signup: %v", err)
	}
	lookup.verified[subjectConfirmed] = false
	pending, err := r.Resolve(t.Context(), identity(subjectConfirmed, newAccountEmail, false))
	if err != nil {
		t.Fatalf("sign-in with an unconfirmed email: %v", err)
	}
	if pending.Email != oldAccountEmail {
		t.Fatalf("unconfirmed email moved the account email to %q", pending.Email)
	}
	lookup.verified[subjectConfirmed] = true
	confirmed, err := r.Resolve(t.Context(), identity(subjectConfirmed, newAccountEmail, false))
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

	user, err := r.Resolve(t.Context(), identity("mover", oldAccountEmail, true))
	if err != nil {
		t.Fatalf("signup: %v", err)
	}
	if _, moveErr := r.Resolve(t.Context(), identity("mover", newAccountEmail, true)); moveErr != nil {
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
