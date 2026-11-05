package validate

import (
	"bytes"
	"testing"
)

func TestValidateCommand(t *testing.T) {
	command := BuildValidateCmd()
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetArgs([]string{"--file", "../../../examples/installation/installation.yaml"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if output.String() != "Installation configuration is valid.\n" {
		t.Fatalf("output: %q", output.String())
	}
}
