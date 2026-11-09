package tvm

import (
	"errors"
)

var (
	ErrDurationExceedsMaxAllowed = errors.New("token duration exceeds maximum allowed")
	ErrStoreToken                = errors.New("unable to store issued token")
	ErrTokenNameTaken            = errors.New("token name already in use for this entity")

	ErrInvalidExpiredToken = errors.New("invalid or expired token")
)
