package httputil

import (
	"net/http"

	"golang.org/x/net/http2"
)

// NewHTTPClient creates an HTTP client with HTTP/2 support enabled.
func NewHTTPClient() *http.Client {
	transport := &http.Transport{}
	err := http2.ConfigureTransport(transport)
	if err != nil {
		panic("failed to configure HTTP/2 transport: " + err.Error())
	}
	return &http.Client{Transport: transport}
}
