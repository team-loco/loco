package main

import (
	"slices"
	"strings"
)

const (
	loginFailurePrefix      = "installation-e2e login failure: "
	loginFailureUnknown     = "unknown"
	loginFailureUnavailable = "api-unavailable"
	loginFailureTLS         = "api-tls"
	loginFailureDNS         = "api-dns"
	loginFailureTransport   = "api-transport"
	loginFailureHTTP        = "api-http-response"
	loginFailureExpired     = "device-code-expired"
)

// classifyLoginFailure maps locoadm login's error output to a category that carries no CLI text.
func classifyLoginFailure(output []byte) string {
	message := cliErrorMessage(output)
	// cmd/internal/login wraps every sign-in error as "login to <host> failed: <cause>".
	if rest, ok := strings.CutPrefix(message, "login to "); ok {
		_, cause, found := strings.Cut(rest, " failed: ")
		if !found {
			return loginFailureUnknown
		}
		message = cause
	}
	// climain.HandleError strips Connect codes; unavailable alone has this fixed prefix.
	if cause, ok := strings.CutPrefix(message, "could not reach the Loco API: "); ok {
		if strings.Contains(cause, "x509:") || strings.Contains(cause, "tls: failed to verify certificate") {
			return loginFailureTLS
		}
		if strings.Contains(cause, ": no such host") || strings.Contains(cause, ": server misbehaving") {
			return loginFailureDNS
		}
		for _, term := range []string{
			": connection refused", ": connection reset by peer",
			"i/o timeout", "context deadline exceeded", "unexpected EOF",
		} {
			if strings.Contains(cause, term) {
				return loginFailureTransport
			}
		}
		return loginFailureUnavailable
	}
	// Connect unary non-JSON errors expose the HTTP status, without a Connect code.
	if message == "404 Not Found" {
		return loginFailureHTTP
	}
	// The exact public message of cmd/internal/login's expired device code.
	if message == "the device code expired before sign-in was approved; run locoadm login --device again" {
		return loginFailureExpired
	}
	return loginFailureUnknown
}

func knownLoginFailure(category string) bool {
	return slices.Contains([]string{
		loginFailureUnknown,
		loginFailureUnavailable,
		loginFailureTLS,
		loginFailureDNS,
		loginFailureTransport,
		loginFailureHTTP,
		loginFailureExpired,
	}, category)
}

func cliErrorMessage(output []byte) string {
	// Fang's non-TTY error is plain text, or a title-cased Error header/details with wrapping.
	message := strings.TrimSuffix(strings.Join(strings.Fields(string(output)), " "), ".")
	message = strings.TrimPrefix(message, "ERROR ")
	if message == "" || message == "ERROR" {
		return ""
	}
	return strings.ToLower(message[:1]) + message[1:]
}
