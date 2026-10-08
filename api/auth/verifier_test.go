package auth

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/team-loco/loco/api/auth/authtest"
)

func TestVerifyMapsNestedClaims(t *testing.T) {
	ti := authtest.NewIssuer(t)
	v := NewVerifier(http.DefaultClient, []IssuerConfig{testConfig(ti)})

	claims := ti.Claims("user-1", "Dev@Example.test", true)
	claims["amr"] = []any{
		map[string]any{"method": "sso/saml", "provider": samlConnectionID},
		"pwd",
	}
	id, err := v.Verify(t.Context(), ti.Sign("k1", claims))
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if id.Issuer != ti.URL() || id.Subject != "user-1" {
		t.Fatalf("identity = %s/%s", id.Issuer, id.Subject)
	}
	if id.Email != "dev@example.test" || !id.EmailVerified {
		t.Fatalf("email = %q verified=%v", id.Email, id.EmailVerified)
	}
	if id.Name != "Test User" || id.AvatarURL != "https://example.com/a.png" {
		t.Fatalf("profile = %q %q", id.Name, id.AvatarURL)
	}
	want := []AuthMethod{{Method: "sso/saml", Provider: samlConnectionID}, {Method: "pwd"}}
	if len(id.Methods) != len(want) || id.Methods[0] != want[0] || id.Methods[1] != want[1] {
		t.Fatalf("methods = %+v", id.Methods)
	}
}

func TestVerifyRejectsBadTokens(t *testing.T) {
	ti := authtest.NewIssuer(t)
	other := authtest.NewIssuer(t)
	v := NewVerifier(http.DefaultClient, []IssuerConfig{testConfig(ti)})

	expired := ti.Claims("u", "u@example.test", true)
	expired["exp"] = time.Now().Add(-time.Minute).Unix()

	wrongAud := ti.Claims("u", "u@example.test", true)
	wrongAud["aud"] = "someone-else"

	noSub := ti.Claims("", "u@example.test", true)

	ti.AddKey("unpublished", false)

	forgedIss := other.Claims("u", "u@example.test", true)
	forgedIss["iss"] = ti.URL()

	cases := []struct {
		name  string
		token string
		want  error
	}{
		{stateExpired, ti.Sign("k1", expired), ErrInvalidToken},
		{"wrong audience", ti.Sign("k1", wrongAud), ErrInvalidToken},
		{"untrusted issuer", other.Sign("k1", other.Claims("u", "u@example.test", true)), ErrUnknownIssuer},
		{"signed by another issuer's key", other.Sign("k1", forgedIss), ErrInvalidToken},
		{"unpublished key", ti.Sign("unpublished", ti.Claims("u", "u@example.test", true)), ErrInvalidToken},
		{"missing subject", ti.Sign("k1", noSub), ErrMissingClaim},
		{"loco token", "loco_s_abc", ErrNotJWT},
		{"garbage", "a.b.c", ErrInvalidToken},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := v.Verify(t.Context(), tc.token)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestVerifyRejectsWrongAudienceForParsedConfig(t *testing.T) {
	ti := authtest.NewIssuer(t)
	issuers, err := ParseIssuers(`[{"issuer":"` + ti.URL() + `","audience":"loco"}]`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	v := NewVerifier(http.DefaultClient, issuers)

	wrongAud := ti.Claims("u", "u@example.test", true)
	wrongAud["aud"] = "someone-else"
	if _, verifyErr := v.Verify(t.Context(), ti.Sign("k1", wrongAud)); !errors.Is(verifyErr, ErrInvalidToken) {
		t.Fatalf("wrong audience err = %v, want %v", verifyErr, ErrInvalidToken)
	}

	rightAud := ti.Claims("u", "u@example.test", true)
	rightAud["aud"] = "loco"
	if _, verifyErr := v.Verify(t.Context(), ti.Sign("k1", rightAud)); verifyErr != nil {
		t.Fatalf("right audience: %v", verifyErr)
	}
}

func TestVerifyPicksUpRotatedKeys(t *testing.T) {
	ti := authtest.NewIssuer(t)
	v := NewVerifier(http.DefaultClient, []IssuerConfig{testConfig(ti)})

	if _, err := v.Verify(t.Context(), ti.Sign("k1", ti.Claims("u", "u@example.test", true))); err != nil {
		t.Fatalf("verify with first key: %v", err)
	}

	ti.AddKey("k2", false)
	ti.Publish("k2")
	if _, err := v.Verify(t.Context(), ti.Sign("k2", ti.Claims("u", "u@example.test", true))); err != nil {
		t.Fatalf("verify with rotated key: %v", err)
	}
}

func TestVerifyUnverifiedEmailUnlessAuthoritative(t *testing.T) {
	ti := authtest.NewIssuer(t)
	token := ti.Sign("k1", ti.Claims("u", "u@corp.test", false))

	plain := NewVerifier(http.DefaultClient, []IssuerConfig{testConfig(ti)})
	id, err := plain.Verify(t.Context(), token)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if id.EmailVerified {
		t.Fatal("unverified email reported as verified")
	}

	authoritative := testConfig(ti)
	authoritative.EmailAuthoritative = true
	id, err = NewVerifier(http.DefaultClient, []IssuerConfig{authoritative}).Verify(t.Context(), token)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !id.EmailVerified {
		t.Fatal("authoritative issuer email not treated as verified")
	}
}

func TestVerifyIgnoresVerificationClaimForAdminVerifiedIssuer(t *testing.T) {
	ti := authtest.NewIssuer(t)
	config := testConfig(ti)
	config.EmailVerification = EmailVerificationAdmin
	claims := ti.Claims("u", "u@example.test", true)
	id, err := NewVerifier(http.DefaultClient, []IssuerConfig{config}).Verify(t.Context(), ti.Sign("k1", claims))
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if id.EmailVerified {
		t.Fatal("token claim trusted for an issuer whose provider confirms emails")
	}
}

func TestLooksLikeJWT(t *testing.T) {
	for token, want := range map[string]bool{
		"a.b.c":         true,
		"loco_s_x.y.z":  false,
		"loco_k_abc":    false,
		"not-a-token":   false,
		"a.b":           false,
		"eyJ.eyJ.sig.x": false,
	} {
		if got := LooksLikeJWT(token); got != want {
			t.Errorf("LooksLikeJWT(%q) = %v, want %v", token, got, want)
		}
	}
}
