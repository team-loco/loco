package rpcerr

import (
	"errors"
	"fmt"

	"connectrpc.com/connect/v2"
)

func New(code connect.Code, cause error) *connect.Error {
	return connect.NewError(code, cause.Error()).WithCause(cause)
}

// CodedError is an expected failure with the code and message a client sees. Callers wrap it
// with private context; Public returns only its own message.
type CodedError struct {
	code   connect.Code
	msg    string
	parent *CodedError
}

func NewCoded(code connect.Code, msg string) *CodedError {
	return &CodedError{code: code, msg: msg}
}

// Detail returns e with public detail appended to its message. errors.Is still matches e.
func (e *CodedError) Detail(format string, args ...any) *CodedError {
	return &CodedError{code: e.code, msg: e.msg + ": " + fmt.Sprintf(format, args...), parent: e}
}

func (e *CodedError) Error() string {
	return e.msg
}

func (e *CodedError) Code() connect.Code {
	return e.code
}

func (e *CodedError) Unwrap() error {
	if e.parent == nil {
		return nil
	}
	return e.parent
}

// Public returns the connect error for the first CodedError in err's chain, with err as the
// private cause.
func Public(err error) (*connect.Error, bool) {
	coded, ok := errors.AsType[*CodedError](err)
	if !ok {
		return nil, false
	}
	return connect.NewError(coded.code, coded.msg).WithCause(err), true
}
