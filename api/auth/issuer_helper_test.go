package auth

import "github.com/team-loco/loco/api/auth/authtest"

func testConfig(ti *authtest.Issuer) IssuerConfig {
	return IssuerConfig{
		Issuer:   ti.URL(),
		JWKSURL:  ti.URL() + "/.well-known/jwks.json",
		Audience: "authenticated",
		Claims: ClaimPaths{
			Subject:       "sub",
			Email:         claimEmail,
			EmailVerified: "user_metadata.email_verified",
			Name:          "user_metadata.full_name",
			AvatarURL:     "user_metadata.avatar_url",
		},
	}
}
