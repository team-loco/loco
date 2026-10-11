package cmdutil

import (
	"errors"
	"fmt"
	"testing"
)

func TestExitCode(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{name: "nil", err: nil, want: 0},
		{name: "plain error", err: errors.New("boom"), want: ExitFailure},
		{name: "exit error", err: &ExitError{Code: ExitChanges}, want: ExitChanges},
		{name: "wrapped exit error", err: fmt.Errorf("plan: %w", &ExitError{Code: ExitChanges}), want: ExitChanges},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ExitCode(tc.err); got != tc.want {
				t.Fatalf("ExitCode(%v) = %d, want %d", tc.err, got, tc.want)
			}
		})
	}
}
