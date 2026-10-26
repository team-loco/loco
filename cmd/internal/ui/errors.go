package ui

import (
	"errors"
)

var (
	ErrNoSelection        = errors.New("no selection made")
	ErrDeploymentCanceled = errors.New("deployment was canceled")

	errUnexpectedModel = errors.New("internal error: unexpected model type")
)
