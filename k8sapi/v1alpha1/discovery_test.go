package v1alpha1

import (
	"strings"
	"testing"
)

func TestPrivateServiceName(t *testing.T) {
	valid := []string{"cart", "payment", "product-catalog", "123cart", strings.Repeat("1", 63)}
	for _, name := range valid {
		if got := PrivateServiceName(name); got != name {
			t.Errorf("PrivateServiceName(%q) = %q", name, got)
		}
	}
	invalid := []string{
		"",
		"My_Service",
		"localhost",
		"resource-01234567-89ab-cdef-0123-456789abcdef",
		"cart.",
		"-cart",
		strings.Repeat("a", 64),
	}
	for _, name := range invalid {
		if got := PrivateServiceName(name); got != "" {
			t.Errorf("PrivateServiceName(%q) = %q, want no alias", name, got)
		}
	}
}
