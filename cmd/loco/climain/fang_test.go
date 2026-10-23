package climain

import (
	"errors"
	"fmt"
	"testing"

	"connectrpc.com/connect/v2"
)

func TestDisplayErrorUsesTheConnectMessage(t *testing.T) {
	unavailable := connect.NewError(connect.CodeUnavailable, "connection refused")
	denied := connect.NewError(connect.CodePermissionDenied, "not a system admin")
	cases := []struct {
		name string
		err  error
		want string
	}{
		{name: "plain error", err: errors.New("boom"), want: "boom"},
		{
			name: "unavailable",
			err:  fmt.Errorf("list: %w", unavailable),
			want: "list: could not reach the Loco API: connection refused",
		},
		{name: "denied", err: denied, want: "not a system admin"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := displayError(tc.err).Error(); got != tc.want {
				t.Fatalf("displayError = %q, want %q", got, tc.want)
			}
		})
	}
}
