package main

import (
	"errors"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/team-loco/loco/gen/go/loco/observability/v1/observabilityv1connect"
)

const observabilityFlushInterval = -time.Nanosecond
const observabilityUnavailable = `{"code":"unavailable","message":"observability is unavailable"}`

var errObservabilityProxyURL = errors.New(
	"OBSERVABILITY_PROXY_URL must be an HTTP(S) origin without credentials, query, or fragment",
)

func parseObservabilityProxyURL(raw string) *url.URL {
	if raw == "" {
		return nil
	}
	target, err := url.Parse(raw)
	if err != nil || (target.Scheme != "http" && target.Scheme != "https") || target.Host == "" || target.User != nil ||
		target.RawQuery != "" ||
		target.Fragment != "" ||
		(target.Path != "" && target.Path != "/") {
		panic(errObservabilityProxyURL)
	}
	return target
}

func mountObservabilityProxy(mux *http.ServeMux, target *url.URL) {
	unavailable := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		if _, err := w.Write([]byte(observabilityUnavailable)); err != nil {
			return
		}
	}
	prefix := "/" + observabilityv1connect.ObservabilityProxyServiceName + "/"
	if target == nil {
		mux.HandleFunc(prefix, unavailable)
		return
	}
	handler := &httputil.ReverseProxy{
		Rewrite:       func(request *httputil.ProxyRequest) { request.SetURL(target); request.SetXForwarded() },
		FlushInterval: observabilityFlushInterval,
		ModifyResponse: func(response *http.Response) error {
			for name := range response.Header {
				if strings.HasPrefix(strings.ToLower(name), "access-control-") {
					response.Header.Del(name)
				}
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, _ error) { unavailable(w, r) },
	}
	mux.Handle(prefix, handler)
}
