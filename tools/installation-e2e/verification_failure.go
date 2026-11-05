package main

const verificationFailurePrefix = "installation-e2e verification failure: "
const verificationFailureUnknown = "unknown"

// These fixed public errors belong to cmd/internal/installation/verify.go.
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
