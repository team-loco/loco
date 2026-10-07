package loco

import "errors"

var (
	errDefinitionRequired = errors.New("infrastructure definition is required")
	errContextRequired    = errors.New("evaluation context requires environment and projectRoot")
	errStackRequired      = errors.New("stack is required")
	errJSONTooLarge       = errors.New("JSON document exceeds the size limit")
)
