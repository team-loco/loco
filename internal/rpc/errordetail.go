package rpc

import (
	"errors"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectproto"
	"google.golang.org/protobuf/proto"
)

// ErrorDetail returns the first detail of type T carried by a Connect error in err's chain.
func ErrorDetail[T proto.Message](err error) (T, bool) {
	var zero T
	connectErr, ok := errors.AsType[*connect.Error](err)
	if !ok {
		return zero, false
	}
	for _, detail := range connectErr.Details() {
		value, decodeErr := connectproto.UnmarshalErrorDetail(detail)
		if decodeErr != nil {
			continue
		}
		if typed, isT := value.(T); isT {
			return typed, true
		}
	}
	return zero, false
}
