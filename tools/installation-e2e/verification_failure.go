package main

import "strings"

const verificationFailurePrefix = "installation-e2e verification failure: "
const verificationFailureUnknown = "unknown"

// these fixed public errors belong to cmd/internal/installation/verify.go.
var verificationFailures = []struct{ message, category string }{
	{"verify source build: installation source verification failed", "source-build"},
	{"verify HTTPS: installation source verification failed", "https"},
	{"verify application logs: installation source verification failed", "application-logs"},
	{"verify encrypted secret storage: installation source verification failed", "secret-storage"},
	{"verify deployed secret delivery: installation source verification failed", "secret-delivery"},
	{"verification resource cleanup failed", "cleanup"},
}

func classifyVerificationFailure(output []byte) string {
	message := cliErrorMessage(output)
	// verifyApplication joins its fixed cleanup error after the failing stage.
	message = strings.TrimSuffix(message, " verification resource cleanup failed")
	for _, known := range verificationFailures {
		if message == known.message {
			return known.category
		}
	}
	return verificationFailureUnknown
}

func knownVerificationFailure(category string) bool {
	if category == verificationFailureUnknown {
		return true
	}
	for _, known := range verificationFailures {
		if category == known.category {
			return true
		}
	}
	return false
}
