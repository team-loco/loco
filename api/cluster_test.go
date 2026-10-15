package main

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

func TestClusterCommandRejectsInvalidArgumentsBeforeConnecting(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{"invalid", "id"},
		{"list", "extra"},
		{"remove"},
		{"pause", "id", "--force"},
		{"remove", "id", "--wrong"},
	} {
		err := runClusterCommand(context.Background(), "invalid database URL", args, new(bytes.Buffer))
		if !errors.Is(err, errClusterUsage) {
			t.Fatalf("args %v: %v", args, err)
		}
	}
}
