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

func TestFilelessSuffix(t *testing.T) {
	first := filelessSuffix("workspace-a", "web", 0)
	if !isFilelessSuffix(first) {
		t.Fatalf("filelessSuffix() = %q, want four lowercase letters", first)
	}
	if got := filelessSuffix("workspace-a", "web", 0); got != first {
		t.Fatalf("filelessSuffix() = %q, want stable %q", got, first)
	}
	if got := filelessSuffix("workspace-a", "web", 1); got == first {
		t.Fatalf("collision retry returned the same suffix %q", got)
	}
}
