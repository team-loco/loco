package auth

import (
	"fmt"
	"strings"
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
	Closed bool
}

func ParseSignupPolicy(mode string) (SignupPolicy, error) {
	mode = strings.TrimSpace(mode)
	if mode != "" && mode != "open" && mode != "closed" {
		return SignupPolicy{}, fmt.Errorf("unknown signup mode %q", mode)
	}
	return SignupPolicy{Closed: mode == "closed"}, nil
}

func (p SignupPolicy) Check() error {
	if p.Closed {
		return rejected("Sign-ups are closed on this installation. Only existing accounts can sign in.")
	}
	return nil
}
