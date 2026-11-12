package auth

import (
	"fmt"
	"strings"
)

type SignupMode string

const (
	SignupOpen   SignupMode = "open"
	SignupClosed SignupMode = "closed"
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
	Mode SignupMode
}

func ParseSignupPolicy(mode string) (SignupPolicy, error) {
	signupMode := SignupMode(strings.TrimSpace(mode))
	if signupMode == "" {
		signupMode = SignupOpen
	}
	p := SignupPolicy{Mode: signupMode}
	switch p.Mode {
	case SignupOpen, SignupClosed:
		return p, nil
	default:
		return SignupPolicy{}, fmt.Errorf("unknown signup mode %q", p.Mode)
	}
}

func (p SignupPolicy) Check() error {
	switch p.Mode {
	case SignupOpen:
		return nil
	case SignupClosed:
		return rejected("Sign-ups are closed on this installation. Only existing accounts can sign in.")
	default:
		return rejected("Sign-ups are not configured.")
	}
}
