package env

import (
	"errors"
	"strings"
	"testing"

	"github.com/team-loco/loco/cmd/internal/dotenv"
)

func TestParseAssignmentsRejectsBadNames(t *testing.T) {
	tests := []struct {
		name string
		arg  string
		want error
	}{
		{name: "empty", arg: "=1", want: dotenv.ErrEmptyName},
		{name: "lowercase", arg: "lower=1", want: dotenv.ErrInvalidName},
		{name: "space", arg: "A B=1", want: dotenv.ErrInvalidName},
		{name: "stdin name", arg: "bad-name", want: dotenv.ErrInvalidName},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseAssignments(strings.NewReader(""), []string{tt.arg})
			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
	}
}
