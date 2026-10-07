package auth

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/team-loco/loco/api/auth/authtest"
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
	if rejected, ok := errors.AsType[*SignupRejectedError](err); !ok {
		t.Fatalf("unverified collision err = %v, want rejection", err)
	} else if rejected.Message == "" {
		t.Fatal("rejection has no message")
	}
	_, err = q.GetUserByIdentity(t.Context(), genDb.GetUserByIdentityParams{
		Issuer: testIssuerURL, Subject: "intruder",
	})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("intruder identity stored: %v", err)
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

const samlConnectionID = "conn-1"

func samlIdentity(sub, email, connection string) Identity {
	id := identity(sub, email, true)
	id.Methods = []AuthMethod{{Method: MethodSAML, Provider: connection}}
	return id
}

func TestResolveTrustsSSOEmailsOnlyOnTheOrgsDomains(t *testing.T) {
	pool := authtest.NewPool(t)
	r := NewResolver(pool, open(t))
	q := genDb.New(pool)
	ctx := t.Context()

	victim, err := r.Resolve(ctx, identity("victim", "victim@victim.test", true))
	if err != nil {
		t.Fatalf("victim: %v", err)
	}
	org, err := q.CreateOrganization(ctx, genDb.CreateOrganizationParams{Name: "corp", CreatedBy: victim.ID})
	if err != nil {
		t.Fatalf("org: %v", err)
	}
	domain, err := q.CreateOrgDomain(
		ctx,
		genDb.CreateOrgDomainParams{OrgID: org.ID, Domain: "corp.test", VerificationToken: "t"},
	)
	if err != nil {
		t.Fatalf("domain: %v", err)
	}
	if _, markErr := q.MarkOrgDomainVerified(
		ctx,
		genDb.MarkOrgDomainVerifiedParams{ID: domain.ID, OrgID: org.ID},
	); markErr != nil {
		t.Fatalf("verify: %v", markErr)
	}
	if _, ssoErr := q.CreateOrgSSO(ctx, genDb.CreateOrgSSOParams{
		OrgID: org.ID, ConnectionID: samlConnectionID, Issuer: testIssuerURL,
	}); ssoErr != nil {
		t.Fatalf("sso: %v", ssoErr)
	}

	_, err = r.Resolve(ctx, samlIdentity("forged", "victim@victim.test", samlConnectionID))
	if rejected, ok := errors.AsType[*SignupRejectedError](err); !ok {
		t.Fatalf("sso login asserting another domain's address = %v, want a rejection", err)
	} else if rejected.Message == "" {
		t.Fatal("rejection has no message")
	}

	verifiedOf := func(sub string) bool {
		t.Helper()
		row, getErr := q.GetIdentity(ctx, genDb.GetIdentityParams{Issuer: testIssuerURL, Subject: sub})
		if getErr != nil {
			t.Fatalf("identity %s: %v", sub, getErr)
		}
		return row.EmailVerified
	}
	if _, resolveErr := r.Resolve(ctx, samlIdentity("alice", "alice@corp.test", samlConnectionID)); resolveErr != nil {
		t.Fatalf("sso login on the org's domain: %v", resolveErr)
	}
	if !verifiedOf("alice") {
		t.Fatal("email on the org's domain was not trusted")
	}
	if _, resolveErr := r.Resolve(ctx, samlIdentity("bob", "bob@corp.test", "conn-unknown")); resolveErr != nil {
		t.Fatalf("sso login through an unknown connection: %v", resolveErr)
	}
	if verifiedOf("bob") {
		t.Fatal("email from an unknown connection was trusted")
	}
}
