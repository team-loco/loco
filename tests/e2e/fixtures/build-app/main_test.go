package main

import (
	"net/http/httptest"
	"testing"
)

func TestWorkspaceResponse(t *testing.T) {
	for _, message := range []string{"workspace-one", "workspace-two"} {
		handler := newHandler(message)
		request := httptest.NewRequestWithContext(t.Context(), "GET", "/", nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if got := response.Body.String(); got != message+"\n" {
			t.Errorf("response = %q, want %q", got, message)
		}
	}
}
