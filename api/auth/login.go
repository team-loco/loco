package auth

import "context"

// Login is what a provider sign-in adds to the request principal: the provider identity and
// the organizations whose pending invitations the sign-in claimed.
type Login struct {
	Identity    Identity
	ClaimedOrgs []ClaimedOrg
}

type loginKey struct{}

func WithLogin(ctx context.Context, l Login) context.Context {
	return context.WithValue(ctx, loginKey{}, l)
}

// LoginFrom reports false for requests authenticated by a Loco token, which carry no
// provider identity.
func LoginFrom(ctx context.Context) (Login, bool) {
	l, ok := ctx.Value(loginKey{}).(Login)
	return l, ok
}
