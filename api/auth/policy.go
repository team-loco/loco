package auth

import (
	"fmt"
	"strings"
)

type SignupRejectedError struct {
	Message string
	// Reason is set when clients show their own text for the rejection.
	Reason string
}

// ReasonSignupClosed marks a sign-up the installation does not accept without an invitation.
const ReasonSignupClosed = "SIGNUP_CLOSED"

func (e *SignupRejectedError) Error() string {
	return e.Message
}

var (
	ErrEmailMissing = &SignupRejectedError{
		Message: "Your identity provider did not share an email address.",
	}
	ErrEmailUnverified = &SignupRejectedError{
		Message: "Verify your email address with your identity provider, then sign in again.",
	}
	ErrEmailTaken = &SignupRejectedError{
		Message: "An account with this email already exists.",
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
		return &SignupRejectedError{
			Message: "Sign-ups are closed on this installation. Only existing accounts can sign in.",
			Reason:  ReasonSignupClosed,
		}
	}
	return nil
}
