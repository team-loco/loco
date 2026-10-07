package auth

import (
	"fmt"
	"slices"
	"strings"
)

type SignupMode string

const (
	SignupOpen    SignupMode = "open"
	SignupDomains SignupMode = "domains"
	SignupClosed  SignupMode = "closed"
)

type SignupRejectedError struct {
	Message string
}

func (e *SignupRejectedError) Error() string {
	return e.Message
}

func rejected(message string) error {
	return &SignupRejectedError{Message: message}
}

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
		return rejected("Sign-ups are closed. Ask an organization admin for an invitation.")
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

func (p SignupPolicy) CheckEmail(email string) error {
	return p.Check(Identity{Email: strings.ToLower(strings.TrimSpace(email)), EmailVerified: true})
}

func emailDomain(email string) string {
	at := strings.LastIndex(email, "@")
	if at < 0 || at == len(email)-1 {
		return ""
	}
	return strings.ToLower(email[at+1:])
}
