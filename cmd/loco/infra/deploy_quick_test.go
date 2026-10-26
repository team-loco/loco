package infra

import (
	"strings"
	"testing"
)

func TestSafeDirectoryName(t *testing.T) {
	cases := []struct {
		directory string
		want      string
		wantErr   bool
	}{
		{directory: "Web App", want: "web-app"},
		{directory: "__API v2!!", want: "api-v2"},
		{directory: "123", want: "123"},
		{directory: "!!!", wantErr: true},
		{directory: strings.Repeat("A", 70), want: strings.Repeat("a", maxDNSLabelLength)},
	}
	for _, test := range cases {
		got, err := safeDirectoryName(test.directory)
		if (err != nil) != test.wantErr || got != test.want {
			t.Errorf(
				"safeDirectoryName(%q) = %q, %v; want %q, error %v",
				test.directory, got, err, test.want, test.wantErr,
			)
		}
	}
}

func TestDomainSuffix(t *testing.T) {
	first := domainSuffix("workspace-a", "web")
	if !isDomainSuffix(first) {
		t.Fatalf("domainSuffix() = %q, want four lowercase letters", first)
	}
	if got := domainSuffix("workspace-a", "web"); got != first {
		t.Fatalf("domainSuffix() = %q, want stable %q", got, first)
	}
	if got := domainSuffix("workspace-b", "web"); got == first {
		t.Fatalf("another workspace returned the same suffix %q", got)
	}
}
