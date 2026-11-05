package locofile

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSlug(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{name: "My App", want: "my-app"},
		{name: "Web App", want: "web-app"},
		{name: "__API v2!!", want: "api-v2"},
		{name: "123", want: "123"},
		{name: "!!!", want: ""},
		{name: strings.Repeat("A", 70), want: strings.Repeat("a", MaxServiceNameLength)},
		{name: strings.Repeat("a", 62) + " b", want: strings.Repeat("a", 62)},
	}
	for _, test := range cases {
		got := Slug(test.name)
		assert.Equal(t, test.want, got, test.name)
		if got != "" {
			assert.True(t, isDNSLabel(got), test.name)
		}
	}
}

func TestIsServiceName(t *testing.T) {
	for _, name := range []string{"web", "Checkout API", "Café", strings.Repeat("é", 62) + "a"} {
		assert.True(t, IsServiceName(name), name)
	}
	for _, name := range []string{"", "!!!", "--", strings.Repeat("a", MaxServiceNameLength+1)} {
		assert.False(t, IsServiceName(name), name)
	}
}
