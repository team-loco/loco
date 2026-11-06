package install

import (
	"errors"
	"testing"
)

func TestInstallationCommandsRejectNonpositiveTimeoutBeforeReadingInputs(t *testing.T) {
	for _, command := range Commands() {
		t.Run(command.Name(), func(t *testing.T) {
			command.SetArgs([]string{"--file", "missing-installation.yaml", "--timeout", "0s"})
			if err := command.Execute(); !errors.Is(err, errTimeout) {
				t.Fatalf("got %v, want invalid timeout before reading configuration", err)
			}
		})
	}
}
