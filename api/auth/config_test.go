package auth

import (
	"errors"
	"testing"
)

const testIssuerURL = "https://auth.example.test"

func TestParseIssuersAppliesDefaults(t *testing.T) {
	issuers, err := ParseIssuers(
		`[{"issuer":"` + testIssuerURL + `","audience":"authenticated","web":{"adapter":"supabase"}}]`,
	)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	ic := issuers[0]
	if ic.JWKSURL != testIssuerURL+"/.well-known/jwks.json" {
		t.Errorf("jwks = %q", ic.JWKSURL)
	}
	if ic.Claims.Subject != "sub" || ic.Claims.Email != claimEmail || ic.Claims.EmailVerified != claimEmailVerified {
		t.Errorf("claims = %+v", ic.Claims)
	}
	if ic.Web.URL != testIssuerURL {
		t.Errorf("web url = %q", ic.Web.URL)
	}
	web, ok := WebIssuer(issuers)
	if !ok || web.Issuer != ic.Issuer {
		t.Errorf("web issuer = %+v %v", web, ok)
	}
}

func TestParseIssuersEmpty(t *testing.T) {
	issuers, err := ParseIssuers("  ")
	if err != nil || issuers != nil {
		t.Fatalf("parse empty = %v %v", issuers, err)
	}
	if _, ok := WebIssuer(nil); ok {
		t.Fatal("web issuer found in empty config")
	}
}

func TestParseIssuersRejectsBadConfig(t *testing.T) {
	for name, raw := range map[string]string{
		"not json":         `{`,
		"missing issuer":   `[{"audience":"loco"}]`,
		"issuer not a url": `[{"issuer":"auth","audience":"loco"}]`,
		"duplicate issuer": `[{"issuer":"https://a.test","audience":"loco"},` +
			`{"issuer":"https://a.test","audience":"loco"}]`,
		"unknown adapter":     `[{"issuer":"https://a.test","audience":"loco","web":{"adapter":"magic"}}]`,
		"oidc without client": `[{"issuer":"https://a.test","audience":"loco","web":{"adapter":"oidc"}}]`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseIssuers(raw); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestParseIssuersRequiresAudience(t *testing.T) {
	_, err := ParseIssuers(`[{"issuer":"` + testIssuerURL + `"}]`)
	if !errors.Is(err, ErrAudienceRequired) {
		t.Fatalf("err = %v, want %v", err, ErrAudienceRequired)
	}
}

func TestSignupPolicy(t *testing.T) {
	verified := func(email string) Identity { return Identity{Email: email, EmailVerified: true} }

	open, err := ParseSignupPolicy("", "")
	if err != nil {
		t.Fatalf("parse open: %v", err)
	}
	if checkErr := open.Check(Identity{Email: "a@b.test"}); checkErr != nil {
		t.Errorf("open rejected: %v", checkErr)
	}

	closed, err := ParseSignupPolicy("closed", "")
	if err != nil {
		t.Fatalf("parse closed: %v", err)
	}
	var rejectedErr *SignupRejectedError
	if checkErr := closed.Check(verified("a@b.test")); !errors.As(checkErr, &rejectedErr) {
		t.Errorf("closed allowed signup: %v", checkErr)
	}

	domains, err := ParseSignupPolicy("domains", " Acme.test , corp.test")
	if err != nil {
		t.Fatalf("parse domains: %v", err)
	}
	if err := domains.Check(verified(devEmail)); err != nil {
		t.Errorf("allowed domain rejected: %v", err)
	}
	if err := domains.Check(verified("dev@evil.test")); !errors.As(err, &rejectedErr) {
		t.Errorf("other domain allowed: %v", err)
	}
	if err := domains.Check(Identity{Email: devEmail}); !errors.As(err, &rejectedErr) {
		t.Errorf("unverified email allowed: %v", err)
	}
	if err := domains.Check(verified("dev@sub.acme.test")); !errors.As(err, &rejectedErr) {
		t.Errorf("subdomain allowed: %v", err)
	}

	for mode, domainList := range map[string]string{"domains": "", "invite-only-typo": ""} {
		if _, err := ParseSignupPolicy(mode, domainList); err == nil {
			t.Errorf("ParseSignupPolicy(%q) accepted", mode)
		}
	}
}

func TestSupabasePreset(t *testing.T) {
	issuers, err := ParseIssuers(
		`[{"issuer":"http://localhost:9999","audience":"authenticated","preset":"supabase",` +
			`"web":{"adapter":"supabase"}}]`,
	)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	ic := issuers[0]
	if ic.Claims.EmailVerified != claimEmailVerified {
		t.Fatalf("preset maps email verification to %q, which users can set themselves", ic.Claims.EmailVerified)
	}
	if ic.Claims.Name != "user_metadata.full_name" || ic.Claims.AvatarURL != "user_metadata.avatar_url" {
		t.Fatalf("preset = %+v", ic)
	}
	_, err = ParseIssuers(`[{"issuer":"http://localhost:9999","audience":"authenticated","preset":"nope"}]`)
	if err == nil {
		t.Fatal("unknown preset accepted")
	}
}

func TestParseIssuersEmailVerification(t *testing.T) {
	issuers, err := ParseIssuers(`[{"issuer":"https://a.test","audience":"loco"}]`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if issuers[0].EmailVerification != EmailVerificationClaim {
		t.Fatalf("default email verification = %q", issuers[0].EmailVerification)
	}
	_, err = ParseIssuers(`[{"issuer":"https://a.test","audience":"loco","emailVerification":"admin"}]`)
	if !errors.Is(err, ErrEmailVerificationNeedsAdmin) {
		t.Fatalf("admin verification without admin: %v", err)
	}
	_, err = ParseIssuers(`[{"issuer":"https://a.test","audience":"loco","emailVerification":"admin",` +
		`"emailAuthoritative":true,"admin":{"type":"supabase","tokenEnv":"KEY"}}]`)
	if !errors.Is(err, ErrEmailVerificationConflict) {
		t.Fatalf("admin verification with authoritative email: %v", err)
	}
	_, err = ParseIssuers(`[{"issuer":"https://a.test","audience":"loco","emailVerification":"magic"}]`)
	if err == nil {
		t.Fatal("unknown email verification accepted")
	}
}
