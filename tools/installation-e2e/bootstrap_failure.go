package main

import (
	"slices"
	"strings"
)

const (
	bootstrapFailurePrefix      = "installation-e2e bootstrap failure: "
	bootstrapFailureUnknown     = "unknown"
	bootstrapFailureUnavailable = "api-unavailable"
	bootstrapFailureTLS         = "api-tls"
	bootstrapFailureDNS         = "api-dns"
	bootstrapFailureTransport   = "api-transport"
	bootstrapFailureIdentity    = "bootstrap-identity-rejected"
	bootstrapFailureToken       = "bootstrap-token-rejected"
	bootstrapFailureServer      = "bootstrap-server"
	bootstrapFailureHTTP        = "api-http-response"
)

func classifyBootstrapFailure(output []byte) string {
	// climain.HandleError strips Connect codes; unavailable alone has this fixed prefix.
	message := cliErrorMessage(output)
	if cause, ok := strings.CutPrefix(message, "could not reach the Loco API: "); ok {
		if strings.Contains(cause, "x509:") || strings.Contains(cause, "tls: failed to verify certificate") {
			return bootstrapFailureTLS
		}
		if strings.Contains(cause, ": no such host") || strings.Contains(cause, ": server misbehaving") {
			return bootstrapFailureDNS
		}
		for _, term := range []string{
			": connection refused", ": connection reset by peer",
			"i/o timeout", "context deadline exceeded", "unexpected EOF",
		} {
			if strings.Contains(cause, term) {
				return bootstrapFailureTransport
			}
		}
		return bootstrapFailureUnavailable
	}
	// Connect unary non-JSON errors expose the HTTP status, without a Connect code.
	if message == "404 Not Found" {
		return bootstrapFailureHTTP
	}
	// These exact public messages belong to api/service/bootstrap.go, not provider diagnostics.
	for _, known := range []struct{ message, category string }{
		{"bootstrap requires a verified identity provider sign-in", bootstrapFailureIdentity},
		{"invalid bootstrap token", bootstrapFailureToken},
		{"bootstrap failed", bootstrapFailureServer},
	} {
		if message == known.message {
			return known.category
		}
	}
	return bootstrapFailureUnknown
}

func knownBootstrapFailure(category string) bool {
	return slices.Contains([]string{
		bootstrapFailureUnknown,
		bootstrapFailureUnavailable,
		bootstrapFailureTLS,
		bootstrapFailureDNS,
		bootstrapFailureTransport,
		bootstrapFailureIdentity,
		bootstrapFailureToken,
		bootstrapFailureServer,
		bootstrapFailureHTTP,
	}, category)
}

func cliErrorMessage(output []byte) string {
	// Fang's non-TTY error is plain text, or a title-cased Error header/details with wrapping.
	message := strings.TrimSuffix(strings.Join(strings.Fields(string(output)), " "), ".")
	if formatted, ok := strings.CutPrefix(message, "ERROR "); ok {
		if formatted == "" {
			return ""
		}
		message = strings.ToLower(formatted[:1]) + formatted[1:]
	}
	return message
}
