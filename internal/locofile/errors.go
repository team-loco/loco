package locofile

import "errors"

var (
	ErrNotSupportedYet        = errors.New("not supported yet")
	ErrEnvironmentsInOverride = errors.New("environments cannot be nested inside an environment override")
	ErrNotAnObject            = errors.New("must be a mapping")
)
