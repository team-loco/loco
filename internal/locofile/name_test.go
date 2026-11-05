package locofile

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestServiceNameFromDir(t *testing.T) {
	cases := []struct {
		directory string
		want      string
	}{
		{directory: "My App", want: "my-app"},
		{directory: "Web App", want: "web-app"},
		{directory: "__API v2!!", want: "api-v2"},
		{directory: "123", want: "123"},
		{directory: "!!!", want: ""},
		{directory: strings.Repeat("A", 70), want: strings.Repeat("a", MaxServiceNameLength)},
		{directory: strings.Repeat("a", 62) + " b", want: strings.Repeat("a", 62)},
	}
	for _, test := range cases {
		got := ServiceNameFromDir(test.directory)
		assert.Equal(t, test.want, got, test.directory)
		if got != "" {
			assert.True(t, IsServiceName(got), test.directory)
		}
	}
}
