package rpcerr

import "connectrpc.com/connect/v2"

func New(code connect.Code, cause error) *connect.Error {
	return connect.NewError(code, cause.Error()).WithCause(cause)
}
