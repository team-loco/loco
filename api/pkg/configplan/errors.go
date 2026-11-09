package configplan

import (
	"errors"

	"connectrpc.com/connect/v2"

	"github.com/team-loco/loco/internal/rpcerr"
)

var (
	ErrOwnedByOtherPartial = errors.New("service is owned by another partial")
	ErrUnknownRegion       = errors.New("region has no active cluster")
	ErrUnknownEnvironment  = errors.New("environment does not exist in the workspace")
	ErrMissingSecret       = rpcerr.NewCoded(
		connect.CodeFailedPrecondition,
		"secret is not set for the service in the environment",
	)
	ErrSecretsTooLarge = rpcerr.NewCoded(
		connect.CodeFailedPrecondition,
		"secrets of the service exceed the size limit of one placement",
	)
	ErrImageUnresolved  = errors.New("image could not be resolved")
	ErrImageNotResolved = errors.New("no resolution was supplied for the image")
)
