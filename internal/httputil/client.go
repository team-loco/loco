package httputil

import (
	"net/http"
)

// NewHTTPClient creates an HTTP client with HTTP/2 support enabled.
func NewHTTPClient() *http.Client {
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetHTTP2(true)
	return &http.Client{Transport: &http.Transport{Protocols: protocols}}
}
