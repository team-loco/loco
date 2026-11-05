package ui

import (
	"errors"
)

var (
	errNoSelection     = errors.New("no selection made")
	errUnexpectedModel = errors.New("internal error: unexpected model type")
)
