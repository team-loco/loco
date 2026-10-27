package service

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestParseOrgLinksAcceptsValidLinks(t *testing.T) {
	raw := `[{"label":"Support","url":"https://support.example.com/orgs/{orgId}/tickets?org={orgId}"},` +
		`{"label":"Status","url":"http://status.example.com"}]`
	links, err := ParseOrgLinks(raw)
	if err != nil {
		t.Fatalf("ParseOrgLinks: %v", err)
	}
	want := []OrgLink{
		{Label: "Support", URL: "https://support.example.com/orgs/{orgId}/tickets?org={orgId}"},
		{Label: "Status", URL: "http://status.example.com"},
	}
	if !slices.Equal(links, want) {
		t.Errorf("links = %+v, want %+v", links, want)
	}
}

func TestParseOrgLinksEmpty(t *testing.T) {
	for _, raw := range []string{"", "  ", "[]"} {
		links, err := ParseOrgLinks(raw)
		if err != nil || len(links) != 0 {
			t.Errorf("ParseOrgLinks(%q) = %+v, %v; want no links", raw, links, err)
		}
	}
}

func TestParseOrgLinksRejectsInvalidLinks(t *testing.T) {
	tooMany := make([]string, maxOrgLinks+1)
	for i := range tooMany {
		label := "L" + strings.Repeat("x", i)
		tooMany[i] = `{"label":"` + label + `","url":"https://example.com"}`
	}
	longLabel := strings.Repeat("a", maxOrgLinkLabelRunes+1)
	tests := []struct {
		name string
		raw  string
		want error
	}{
		{"too many links", "[" + strings.Join(tooMany, ",") + "]", errTooManyOrgLinks},
		{"empty label", `[{"label":"","url":"https://example.com"}]`, errOrgLinkLabel},
		{"blank label", `[{"label":"   ","url":"https://example.com"}]`, errOrgLinkLabel},
		{"long label", `[{"label":"` + longLabel + `","url":"https://example.com"}]`, errOrgLinkLabel},
		{
			"duplicate label",
			`[{"label":"A","url":"https://a.example.com"},{"label":"A","url":"https://b.example.com"}]`,
			errDuplicateOrgLink,
		},
		{"relative url", `[{"label":"A","url":"/orgs/{orgId}"}]`, errOrgLinkURL},
		{"javascript url", `[{"label":"A","url":"javascript:alert(1)"}]`, errOrgLinkURL},
		{"url with credentials", `[{"label":"A","url":"https://user:pass@example.com"}]`, errOrgLinkURL},
		{"empty url", `[{"label":"A","url":""}]`, errOrgLinkURL},
		{"unknown placeholder", `[{"label":"A","url":"https://example.com/{workspaceId}"}]`, errOrgLinkPlaceholder},
		{"unclosed placeholder", `[{"label":"A","url":"https://example.com/{orgId"}]`, errOrgLinkPlaceholder},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseOrgLinks(tt.raw)
			if !errors.Is(err, tt.want) {
				t.Errorf("err = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestParseOrgLinksRejectsMalformedJSON(t *testing.T) {
	for _, raw := range []string{"{", `[{"label":"A","url":"https://example.com","icon":"x"}]`} {
		if _, err := ParseOrgLinks(raw); err == nil {
			t.Errorf("ParseOrgLinks(%q) succeeded", raw)
		}
	}
}
