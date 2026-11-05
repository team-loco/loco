package rpcretry

import "connectrpc.com/connect/v2"

// Retryable reports whether a control-plane RPC error can clear on its own. A rejected
// credential or an exhausted quota stays until a person acts, so retrying only hides it.
func Retryable(err error) bool {
	switch connect.CodeOf(err) {
	case connect.CodePermissionDenied:
		return false
	case connect.CodeUnauthenticated:
		return false
	case connect.CodeResourceExhausted:
		return false
	default:
		return true
	}
}
