package tvm

import (
	"errors"
)

var (
	ErrDurationExceedsMaxAllowed = errors.New("token duration exceeds maximum allowed")
	ErrInsufficentPermissions    = errors.New("insufficient permissions")
	ErrStoreToken                = errors.New("unable to store issued token")
	ErrImproperUsage             = errors.New("improper usage of token vending machine")

	ErrTokenExpired        = errors.New("token has expired")
	ErrTokenNotFound       = errors.New("token not found")
	ErrInvalidExpiredToken = errors.New("invalid or expired token")

	ErrExchange = errors.New("exchange with external provider failed")

	ErrUserNotFound   = errors.New("user not found")
	ErrEntityNotFound = errors.New("entity not found or invalid entity")

	ErrIssueToken = errors.New("unable to issue token")
)
