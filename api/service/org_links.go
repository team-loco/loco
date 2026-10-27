package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"unicode/utf8"

	configv1 "github.com/team-loco/loco/gen/go/loco/config/v1"
)

const (
	orgIDPlaceholder     = "{orgId}"
	orgLinkSampleID      = "0"
	maxOrgLinks          = 10
	maxOrgLinkLabelRunes = 32
)

var (
	errTooManyOrgLinks    = errors.New("too many org links")
	errOrgLinkLabel       = errors.New("org link label must be non-empty and short")
	errDuplicateOrgLink   = errors.New("org link labels must be unique")
	errOrgLinkURL         = errors.New("org link url must be an absolute http or https url")
	errOrgLinkPlaceholder = errors.New("org link url may only contain the " + orgIDPlaceholder + " placeholder")
)

type OrgLink struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

func ParseOrgLinks(raw string) ([]OrgLink, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var links []OrgLink
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&links); err != nil {
		return nil, fmt.Errorf("decode org links: %w", err)
	}
	if len(links) > maxOrgLinks {
		return nil, fmt.Errorf("%w: %d, at most %d", errTooManyOrgLinks, len(links), maxOrgLinks)
	}
	seen := map[string]bool{}
	for i, link := range links {
		if err := link.validate(); err != nil {
			return nil, fmt.Errorf("org link %d: %w", i, err)
		}
		if seen[link.Label] {
			return nil, fmt.Errorf("%w: %q", errDuplicateOrgLink, link.Label)
		}
		seen[link.Label] = true
	}
	return links, nil
}

func (l OrgLink) validate() error {
	label := strings.TrimSpace(l.Label)
	if label == "" || utf8.RuneCountInString(label) > maxOrgLinkLabelRunes {
		return fmt.Errorf("%w: %q, at most %d characters", errOrgLinkLabel, l.Label, maxOrgLinkLabelRunes)
	}
	expanded := strings.ReplaceAll(l.URL, orgIDPlaceholder, orgLinkSampleID)
	if strings.ContainsAny(expanded, "{}") {
		return fmt.Errorf("%w: %q", errOrgLinkPlaceholder, l.URL)
	}
	u, err := url.Parse(expanded)
	if err != nil || u.Hostname() == "" || u.User != nil || (u.Scheme != schemeHTTP && u.Scheme != schemeHTTPS) {
		return fmt.Errorf("%w: %q", errOrgLinkURL, l.URL)
	}
	return nil
}

func orgLinksProto(links []OrgLink) []*configv1.OrgLink {
	out := make([]*configv1.OrgLink, len(links))
	for i, link := range links {
		out[i] = &configv1.OrgLink{Label: link.Label, Url: link.URL}
	}
	return out
}
