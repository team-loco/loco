// Package endpointurl validates the address of an HTTP server.
package endpointurl

import (
	"errors"
	"net/url"
	"strings"
)

var ErrInvalid = errors.New("must be an http:// or https:// URL with a host and without credentials, query or fragment")

// Parse returns raw as a URL when it is an HTTP or HTTPS address with a host and no credentials, query or fragment.
func Parse(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, ErrInvalid
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, ErrInvalid
	}
	if u.Hostname() == "" {
		return nil, ErrInvalid
	}
	if u.User != nil {
		return nil, ErrInvalid
	}
	if u.RawQuery != "" || u.ForceQuery {
		return nil, ErrInvalid
	}
	if strings.Contains(raw, "#") {
		return nil, ErrInvalid
	}
	return u, nil
}
