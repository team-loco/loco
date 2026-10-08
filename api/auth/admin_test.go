package auth

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

const (
	fakeAdminType       = "fake"
	adminIssuer         = "https://a.test"
	adminURL            = "https://admin.a.test"
	adminTokenEnv       = "SERVICE_KEY"
	adminToken          = "service-key"
	emailVerifierIssuer = "https://verifier.test"
	subjectConfirmed    = "confirmed"
	stateExpired        = "expired"
)

type fakeAdmin struct {
	baseURL string
	token   string
	client  *http.Client
}

func (*fakeAdmin) State(context.Context, string) (IdentityState, error) {
	return IdentityActive, nil
}

func (*fakeAdmin) Delete(context.Context, string) error {
	return nil
}

func fakeFactories() AdminFactories {
	return AdminFactories{fakeAdminType: func(baseURL, token string, client *http.Client) IdentityAdmin {
		return &fakeAdmin{baseURL: baseURL, token: token, client: client}
	}}
}

func TestNewAdminsBuildsFromFactory(t *testing.T) {
	client := &http.Client{}
	admins, err := NewAdmins(client, []IssuerConfig{
		{Issuer: adminIssuer, Admin: &AdminConfig{
			Type: fakeAdminType, URL: adminURL, TokenEnv: adminTokenEnv, Token: adminToken,
		}},
		{Issuer: emailVerifierIssuer},
	}, fakeFactories())
	if err != nil {
		t.Fatalf("admins: %v", err)
	}
	if len(admins) != 1 {
		t.Fatalf("admins = %v, want one", admins)
	}
	admin, ok := admins.For(adminIssuer)
	if !ok {
		t.Fatal("no admin for issuer")
	}
	fake, ok := admin.(*fakeAdmin)
	if !ok {
		t.Fatalf("admin = %T", admin)
	}
	if fake.baseURL != adminURL || fake.token != adminToken || fake.client != client {
		t.Fatalf("factory got %+v", fake)
	}
}

func TestNewAdminsValidation(t *testing.T) {
	if _, err := NewAdmins(http.DefaultClient, []IssuerConfig{{
		Issuer: adminIssuer, Admin: &AdminConfig{Type: fakeAdminType, TokenEnv: "MISSING"},
	}}, fakeFactories()); !errors.Is(err, ErrAdminTokenMissing) {
		t.Fatalf("empty token: err = %v, want %v", err, ErrAdminTokenMissing)
	}
	if _, err := NewAdmins(http.DefaultClient, []IssuerConfig{{
		Issuer: adminIssuer, Admin: &AdminConfig{Type: "okta", TokenEnv: "X", Token: adminToken},
	}}, fakeFactories()); err == nil {
		t.Fatal("unknown admin type accepted")
	}
}

func TestNewEmailVerifiersNeedsAnAdmin(t *testing.T) {
	_, err := NewEmailVerifiers([]IssuerConfig{{
		Issuer: emailVerifierIssuer, EmailVerification: EmailVerificationAdmin,
	}}, Admins{})
	if !errors.Is(err, ErrNoEmailVerifier) {
		t.Fatalf("err = %v, want %v", err, ErrNoEmailVerifier)
	}
	verifiers, err := NewEmailVerifiers([]IssuerConfig{{Issuer: emailVerifierIssuer}}, Admins{})
	if err != nil || len(verifiers) != 0 {
		t.Fatalf("claim issuer: %v %v", verifiers, err)
	}
}
