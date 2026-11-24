package appdomain

import (
	"connectrpc.com/connect/v2"

	"github.com/team-loco/loco/internal/rpcerr"
)

var (
	ErrCustomDomain = rpcerr.NewCoded(
		connect.CodeInvalidArgument,
		"domain is not under an app domain any cluster serves",
	)
	ErrDomainNotServed = rpcerr.NewCoded(
		connect.CodeFailedPrecondition,
		"a cluster the service deploys to does not serve the domain",
	)
	ErrNotSingleLabel = rpcerr.NewCoded(
		connect.CodeInvalidArgument,
		"domain must be a single label under its app domain",
	)
	ErrInvalidLabel = rpcerr.NewCoded(
		connect.CodeInvalidArgument,
		"domain label must be an RFC 1123 DNS label: lowercase letters, digits and hyphens, "+
			"1 to 63 characters, starting and ending with a letter or digit",
	)
)
