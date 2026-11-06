package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMailFixtureAcceptsBoundedNativeHTTPPosts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(mailSinkRequest))
	defer server.Close()
	for _, test := range []struct {
		name, method, body string
		status             int
	}{
		{"invitation", http.MethodPost, `{"type":"invitation"}`, http.StatusNoContent},
		{"wrong method", http.MethodGet, "", http.StatusMethodNotAllowed},
		{"oversized", http.MethodPost, strings.Repeat("x", mailBodyLimit+1), http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			request, err := http.NewRequestWithContext(
				t.Context(),
				test.method,
				server.URL,
				strings.NewReader(test.body),
			)
			if err != nil {
				t.Fatal(err)
			}
			response, err := server.Client().Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != test.status {
				t.Fatalf("HTTP fixture returned %d, want %d", response.StatusCode, test.status)
			}
		})
	}
}
