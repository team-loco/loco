package main

import (
	"slices"
	"testing"
)

func TestWebCmdCompletesEveryPage(t *testing.T) {
	cmd := newWebCmd()
	for _, name := range webPageNames() {
		if !slices.Contains(cmd.ValidArgs, name) {
			t.Errorf("ValidArgs is missing %q", name)
		}
		if _, ok := findWebPage(name); !ok {
			t.Errorf("findWebPage(%q) not found", name)
		}
	}
	for _, alias := range []string{"account", "orgs"} {
		if !slices.Contains(cmd.ValidArgs, alias) {
			t.Errorf("ValidArgs is missing alias %q", alias)
		}
	}
}

func TestWebPagePaths(t *testing.T) {
	cases := []struct{ page, org, ws, want string }{
		{"dashboard", "o1", "w1", "/org/o1/wks/w1/dashboard"},
		{"dashboard", "", "", "/dashboard"},
		{"org-settings", "o1", "w1", "/org/o1/settings"},
		{"org-settings", "", "", "/organizations"},
		{"account", "o1", "w1", "/profile"},
		{"orgs", "o1", "w1", "/organizations"},
	}
	for _, c := range cases {
		p, ok := findWebPage(c.page)
		if !ok {
			t.Fatalf("page %q not found", c.page)
		}
		if got := p.path(c.org, c.ws); got != c.want {
			t.Errorf("%s(%q,%q) = %q, want %q", c.page, c.org, c.ws, got, c.want)
		}
	}
}
