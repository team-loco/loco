package configplan

import (
	"errors"

	"connectrpc.com/connect/v2"

	"github.com/team-loco/loco/internal/rpcerr"
)

var (
	ErrOwnedByOtherPartial = errors.New("service is owned by another partial")
	ErrUnknownRegion       = errors.New("region has no healthy cluster accepting applications")
	ErrUnknownEnvironment  = errors.New("environment does not exist in the workspace")
	ErrSecretsTooLarge     = rpcerr.NewCoded(
		connect.CodeFailedPrecondition,
		"the secret values one placement of the service reads exceed the size limit",
	)
	ErrImageUnresolved  = errors.New("image could not be resolved")
	ErrImageNotResolved = errors.New("no resolution was supplied for the image")
)
