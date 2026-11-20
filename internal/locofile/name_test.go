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
		{name: "Café Orders", want: "cafe-orders"},
		{name: "Zürich", want: "zurich"},
		{name: "Crème Brûlée ☕", want: "creme-brulee"},
		{name: strings.Repeat("A", 70), want: strings.Repeat("a", MaxServiceNameLength)},
		{name: strings.Repeat("a", 62) + " b", want: strings.Repeat("a", 62)},
	}
	for _, test := range cases {
		got := Slug(test.name)
		assert.Equal(t, test.want, got, test.name)
		if got != "" {
			assert.True(t, IsDNSLabel(got), test.name)
		}
	}
}

func TestIsDNSLabel(t *testing.T) {
	for _, name := range []string{"web", "123cart", "qa-echo", strings.Repeat("a", MaxServiceNameLength)} {
		assert.True(t, IsDNSLabel(name), name)
	}
	for _, name := range []string{
		"", "!!!", "--", "-bad-", "Checkout API", "Café", "🚀rocket", strings.Repeat("a", MaxServiceNameLength+1),
	} {
		assert.False(t, IsDNSLabel(name), name)
	}
}
