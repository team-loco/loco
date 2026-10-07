package auth

import (
	"errors"
	"testing"
)

const testIssuerURL = "https://auth.example.test"

func TestParseIssuersAppliesDefaults(t *testing.T) {
	issuers, err := ParseIssuers(`[{"issuer":"` + testIssuerURL + `","web":{"adapter":"supabase"}}]`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	ic := issuers[0]
	if ic.JWKSURL != testIssuerURL+"/.well-known/jwks.json" {
		t.Errorf("jwks = %q", ic.JWKSURL)
	}
	if ic.Claims.Subject != "sub" || ic.Claims.Email != claimEmail || ic.Claims.EmailVerified != "email_verified" {
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
		"not json":            `{`,
		"missing issuer":      `[{}]`,
		"issuer not a url":    `[{"issuer":"auth"}]`,
		"duplicate issuer":    `[{"issuer":"https://a.test"},{"issuer":"https://a.test"}]`,
		"unknown adapter":     `[{"issuer":"https://a.test","web":{"adapter":"magic"}}]`,
		"oidc without client": `[{"issuer":"https://a.test","web":{"adapter":"oidc"}}]`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseIssuers(raw); err == nil {
				t.Fatal("expected an error")
			}
		})
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
