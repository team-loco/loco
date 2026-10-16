package token

import (
	"testing"
)

func TestParseTokenDuration(t *testing.T) {
	cases := []struct {
		input   string
		seconds int64
	}{
		{input: "1d", seconds: 86400},
		{input: "7d", seconds: 604800},
		{input: " 2D ", seconds: 172800},
		{input: "+1d", seconds: 86400},
		{input: "-1d", seconds: -86400},
		{input: "0d", seconds: 0},
		{input: "1h30m", seconds: 5400},
		{input: "1.5h", seconds: 5400},
		{input: "1m", seconds: 60},
		{input: "106751991167300d", seconds: 9223372036854720000},
		{input: "-106751991167300d", seconds: -9223372036854720000},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			seconds, err := parseDuration(tc.input)
			if err != nil || seconds != tc.seconds {
				t.Fatalf("parseDuration(%q) = %d, %v; want %d", tc.input, seconds, err, tc.seconds)
			}
		})
	}
}

func TestRejectMalformedTokenDuration(t *testing.T) {
	invalidDurations := []string{
		"1garbaged",
		"1.5d",
		"1dd",
		"1 d",
		"1e1d",
		"0x10d",
		"1h1d",
		"d",
		"",
		"213503982334602d",
		"106751991167301d",
		"-106751991167301d",
		"9223372036854775807d",
		"-9223372036854775808d",
		"9223372036854775808d",
	}
	for _, input := range invalidDurations {
		t.Run(input, func(t *testing.T) {
			seconds, err := parseDuration(input)
			if err == nil {
				t.Fatalf("invalid duration %q accepted as %d seconds", input, seconds)
			}
		})
	}
}

func TestTokenCreateRejectsInvalidDurationBeforeRPC(t *testing.T) {
	for _, duration := range []string{"1garbaged", "213503982334602d"} {
		t.Run(duration, func(t *testing.T) {
			fake := &tokenIDsServer{t: t}
			cmd := tokenIDCommand(t, fake, testCreateAction)
			cmd.SilenceErrors = true
			cmd.SilenceUsage = true
			cmd.SetArgs(
				[]string{
					testTokenName,
					"--entity-type",
					testWorkspaceEntity,
					"--entity-id",
					testEntityID,
					"--expires",
					duration,
				},
			)
			if err := cmd.Execute(); err == nil {
				t.Fatal("invalid duration created a token")
			}
			if fake.calls != 0 {
				t.Fatalf("invalid duration made %d CreateToken calls", fake.calls)
			}
		})
	}
}
