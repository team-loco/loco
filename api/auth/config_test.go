package auth

import (
	"errors"
	"testing"
)

const testIssuerURL = "https://auth.example.test"

func TestParseIssuersAppliesDefaults(t *testing.T) {
	issuers, err := ParseIssuers(
		`[{"issuer":"` + testIssuerURL + `","audience":"loco","web":{"clientId":"loco"}}]`,
	)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	ic := issuers[0]
	if ic.JWKSURL != "" {
		t.Errorf("jwks = %q, want discovery", ic.JWKSURL)
	}
	if ic.Claims.Subject != "sub" || ic.Claims.Email != claimEmail || ic.Claims.EmailVerified != claimEmailVerified {
		t.Errorf("claims = %+v", ic.Claims)
	}
	if ic.Web.Scopes != defaultWebScopes {
		t.Errorf("web scopes = %q", ic.Web.Scopes)
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
		"web without client": `[{"issuer":"https://a.test","audience":"loco","web":{}}]`,
		"web with url": `[{"issuer":"https://a.test","audience":"loco",` +
			`"web":{"clientId":"loco","url":"https://a.test"}}]`,
		"web with adapter": `[{"issuer":"https://a.test","audience":"loco",` +
			`"web":{"adapter":"oidc","clientId":"loco"}}]`,
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
	for _, tc := range []struct {
		mode   string
		closed bool
	}{
		{mode: ""},
		{mode: "open"},
		{mode: " open "},
		{mode: "closed", closed: true},
		{mode: " closed ", closed: true},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			policy, err := ParseSignupPolicy(tc.mode)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if policy.Closed != tc.closed {
				t.Fatalf("closed = %v, want %v", policy.Closed, tc.closed)
			}
			err = policy.Check()
			if !tc.closed {
				if err != nil {
					t.Fatalf("open rejected: %v", err)
				}
				return
			}
			if _, ok := errors.AsType[*SignupRejectedError](err); !ok {
				t.Fatalf("closed allowed a signup: %v", err)
			}
		})
	}

	if _, err := ParseSignupPolicy("invite-only-typo"); err == nil {
		t.Error("accepted unknown signup mode")
	}
}

func TestParseIssuersRejectsUnknownFields(t *testing.T) {
	_, err := ParseIssuers(`[{"issuer":"http://localhost:9999","audience":"loco","preset":"supabase"}]`)
	if err == nil {
		t.Fatal("unknown field accepted")
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
