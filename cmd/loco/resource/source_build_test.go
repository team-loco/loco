package resource

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"syscall"
	"testing"
)

func TestConnectionDropped(t *testing.T) {
	tests := map[string]struct {
		err  error
		want bool
	}{
		"connection reset":         {syscall.ECONNRESET, true},
		"broken pipe":              {syscall.EPIPE, true},
		"closed while writing":     {net.ErrClosed, true},
		"unexpected end of stream": {io.ErrUnexpectedEOF, true},
		"end of stream":            {io.EOF, true},
		"timeout":                  {http.ErrHandlerTimeout, false},
		"other":                    {errors.New("dial failed"), false},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			opErr := &net.OpError{Op: "write", Net: "tcp", Err: tc.err}
			wrapped := fmt.Errorf("readfrom tcp: %w", opErr)
			err := &url.Error{Op: "Put", URL: "http://127.0.0.1/upload", Err: wrapped}
			if got := connectionDropped(err); got != tc.want {
				t.Errorf("connectionDropped(%v) = %v, want %v", err, got, tc.want)
			}
		})
	}
}
