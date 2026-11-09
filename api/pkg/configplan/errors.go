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
	ErrCustomDomain = rpcerr.NewCoded(
		connect.CodeInvalidArgument,
		"domain is not under a platform domain any cluster serves",
	)
	ErrDomainNotServed = rpcerr.NewCoded(
		connect.CodeFailedPrecondition,
		"a cluster the service deploys to does not serve the domain",
	)
	ErrNotSingleLabel = rpcerr.NewCoded(
		connect.CodeInvalidArgument,
		"domain must be a single label under its platform domain",
	)
	ErrImageUnresolved  = errors.New("image could not be resolved")
	ErrImageNotResolved = errors.New("no resolution was supplied for the image")
)
