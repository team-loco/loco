package auth

import (
	"context"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
)

const (
	WebSessionCookie = "loco_session"
	WebRefreshCookie = "loco_refresh"
	WebLoginCookie   = "loco_login"
	httpsScheme      = "https"
	httpScheme       = "http"
)

// WebCookies describes the dashboard cookies and the origins permitted to use them.
type WebCookies struct {
	origin         string
	allowedOrigins []string
	secure         bool
}

func NewWebCookies(webURL string, allowedOrigins []string) WebCookies {
	u, err := url.Parse(webURL)
	if err != nil {
		return WebCookies{}
	}
	origin := webOrigin(u)
	if origin == "" {
		return WebCookies{}
	}

	hostname := strings.ToLower(u.Hostname())
	ip, ipErr := netip.ParseAddr(hostname)
	return WebCookies{
		origin:         origin,
		allowedOrigins: allowedOrigins,
		secure:         u.Scheme == httpsScheme || hostname == "localhost" || (ipErr == nil && ip.IsLoopback()),
	}
}

func webOrigin(u *url.URL) string {
	if u.Hostname() == "" || (u.Scheme != httpsScheme && u.Scheme != httpScheme) {
		return ""
	}
	host := strings.ToLower(u.Host)
	if u.Scheme == httpsScheme && u.Port() == "443" || u.Scheme == httpScheme && u.Port() == "80" {
		host = strings.TrimSuffix(host, ":"+u.Port())
	}
	return u.Scheme + "://" + host
}

func (c WebCookies) Origin() string { return c.origin }

func (c WebCookies) Read(headers interface{ Values(string) []string }, name string) string {
	request := http.Request{Header: http.Header{"Cookie": headers.Values("Cookie")}}
	cookies := request.CookiesNamed(c.name(name))
	if len(cookies) != 1 {
		return ""
	}
	return cookies[0].Value
}

// Set writes a session cookie. A zero expiry clears the cookie.
func (c WebCookies) Set(headers *connect.Header, name, value string, expires time.Time, sameSite http.SameSite) {
	cookie := &http.Cookie{ //nolint:gosec // A non-loopback HTTP dashboard cannot use Secure cookies.
		Name:     c.name(name),
		Value:    value,
		Path:     "/",
		Secure:   c.secure,
		HttpOnly: true,
		SameSite: sameSite,
		Expires:  expires,
	}
	if expires.IsZero() {
		cookie.MaxAge = -1
	}
	headers.Add("Set-Cookie", cookie.String())
}

// AllowsRequest checks for the Connect header and, for requests with an Origin,
// matches that Origin against the configured dashboard origins. Browser GET
// navigations may omit Origin, so a cookie-authenticated GET requires one.
func (c WebCookies) AllowsRequest(ctx context.Context, headers interface{ Values(string) []string }) bool {
	if c.origin == "" {
		return false
	}
	protocolVersions := headers.Values("Connect-Protocol-Version")
	if len(protocolVersions) != 1 || protocolVersions[0] != "1" {
		return false
	}

	server, _ := connecthttp.ServerInfoForContext(ctx)
	origins := headers.Values("Origin")
	switch len(origins) {
	case 0:
		return server.HTTPMethod() == http.MethodPost
	case 1:
		origin := origins[0]
		if !strings.HasPrefix(origin, httpsScheme+"://") && !strings.HasPrefix(origin, httpScheme+"://") {
			return false
		}
		return origin == c.origin || slices.Contains(c.allowedOrigins, origin)
	default:
		return false
	}
}

func (c WebCookies) name(name string) string {
	if c.secure {
		return "__Host-" + name
	}
	return name
}
