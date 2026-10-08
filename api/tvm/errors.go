package tvm

import (
	"errors"
)

var (
	ErrDurationExceedsMaxAllowed = errors.New("token duration exceeds maximum allowed")
	ErrStoreToken                = errors.New("unable to store issued token")
	ErrTokenNameTaken            = errors.New("token name already in use for this entity")
	ErrImproperUsage             = errors.New("improper usage of token vending machine")

	ErrTokenExpired        = errors.New("token has expired")
	ErrTokenNotFound       = errors.New("token not found")
	ErrInvalidExpiredToken = errors.New("invalid or expired token")

	ErrIssueToken = errors.New("unable to issue token")
)
