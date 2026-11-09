package platformdomain

import (
	"connectrpc.com/connect/v2"

	"github.com/team-loco/loco/internal/rpcerr"
)

var (
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
)
