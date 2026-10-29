package auth

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

type SignupMode string

const (
	SignupOpen    SignupMode = "open"
	SignupDomains SignupMode = "domains"
	SignupClosed  SignupMode = "closed"

	removedSignupInvite SignupMode = "invite"
)

var errInviteModeRemoved = errors.New(`signup mode "invite" was removed; use "closed" to stop new accounts`)

type SignupRejectedError struct {
	Message string
}

func (e *SignupRejectedError) Error() string {
	return e.Message
}

func rejected(message string) error {
	return &SignupRejectedError{Message: message}
}

var (
	ErrEmailMissing = &SignupRejectedError{
		Message: "Your identity provider did not share an email address.",
	}
	ErrEmailUnverified = &SignupRejectedError{
		Message: "Verify your email address with your identity provider, then sign in again.",
	}
	ErrEmailTaken = &SignupRejectedError{
		Message: "An account with this email already exists. Sign in the way you did before.",
	}
)

type SignupPolicy struct {
	Mode    SignupMode
	Domains []string
}

func ParseSignupPolicy(mode string, domains string) (SignupPolicy, error) {
	p := SignupPolicy{Mode: SignupMode(strings.TrimSpace(mode))}
	if p.Mode == "" {
		p.Mode = SignupOpen
	}
	for d := range strings.SplitSeq(domains, ",") {
		d = strings.ToLower(strings.TrimSpace(d))
		if d != "" {
			p.Domains = append(p.Domains, d)
		}
	}
	switch p.Mode {
	case SignupOpen:
		return p, nil
	case SignupClosed:
		return p, nil
	case removedSignupInvite:
		return SignupPolicy{}, errInviteModeRemoved
	case SignupDomains:
		if len(p.Domains) == 0 {
			return SignupPolicy{}, fmt.Errorf("signup mode %q needs at least one domain", p.Mode)
		}
		return p, nil
	default:
		return SignupPolicy{}, fmt.Errorf("unknown signup mode %q", p.Mode)
	}
}

func (p SignupPolicy) Check(id Identity) error {
	switch p.Mode {
	case SignupOpen:
		return nil
	case SignupClosed:
		return rejected("Sign-ups are closed on this installation. Only existing accounts can sign in.")
	case SignupDomains:
		domain := emailDomain(id.Email)
		if !id.EmailVerified || domain == "" {
			return rejected("Sign-ups need a verified email address.")
		}
		if slices.Contains(p.Domains, domain) {
			return nil
		}
		return rejected(fmt.Sprintf("Sign-ups from %s are not allowed.", domain))
	default:
		return rejected("Sign-ups are not configured.")
	}
}

func emailDomain(email string) string {
	at := strings.LastIndex(email, "@")
	if at < 0 || at == len(email)-1 {
		return ""
	}
	return strings.ToLower(email[at+1:])
}
