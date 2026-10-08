package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/tvm"
	"github.com/team-loco/loco/api/tvm/providers"
)

func TestGithubSignupCreatesIdentityAndExchangeFindsIt(t *testing.T) {
	f := newDeployFixture(t)
	ctx := context.Background()
	machine := tvm.NewVendingMachine(f.pool, f.queries, tvm.Config{
		SessionAccessTokenDuration:  time.Hour,
		SessionRefreshTokenDuration: time.Hour,
		LastUsedUpdateInterval:      time.Minute,
	})
	t.Cleanup(machine.Close)
	s := &OAuthServer{db: f.pool, queries: f.queries, machine: machine}

	identity := providers.NewEmailResponse(providers.GithubIssuer, "42", "octo@example.com", true, nil)
	created, err := s.tempCreateUser(ctx, identity, "Octo", "https://example.com/octo.png")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	found, err := f.queries.GetUserByIdentity(ctx, genDb.GetUserByIdentityParams{
		Issuer:  providers.GithubIssuer,
		Subject: "42",
	})
	if err != nil {
		t.Fatalf("get user by identity: %v", err)
	}
	if found.User.ID != created.ID {
		t.Fatalf("identity resolved to user %s, want %s", found.User.ID, created.ID)
	}

	user, access, refresh, err := machine.Exchange(ctx, identity, "", "")
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if user.ID != created.ID || access == "" || refresh == "" {
		t.Fatalf("exchange returned user %s with tokens %q %q", user.ID, access, refresh)
	}
}

func TestGithubSignupWithTakenEmailLeavesNoIdentity(t *testing.T) {
	f := newDeployFixture(t)
	ctx := context.Background()
	s := &OAuthServer{db: f.pool, queries: f.queries}

	first := providers.NewEmailResponse(providers.GithubIssuer, "42", "octo@example.com", true, nil)
	if _, err := s.tempCreateUser(ctx, first, "Octo", ""); err != nil {
		t.Fatalf("create first user: %v", err)
	}

	second := providers.NewEmailResponse(providers.GithubIssuer, "43", "octo@example.com", true, nil)
	if _, err := s.tempCreateUser(ctx, second, "Octo Two", ""); !errors.Is(err, ErrEmailAlreadyRegistered) {
		t.Fatalf("second signup error = %v, want ErrEmailAlreadyRegistered", err)
	}

	_, err := f.queries.GetUserByIdentity(ctx, genDb.GetUserByIdentityParams{
		Issuer:  providers.GithubIssuer,
		Subject: "43",
	})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("identity for rejected signup: err = %v, want no rows", err)
	}
}
