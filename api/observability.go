package main

import (
	"errors"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/team-loco/loco/api/reqctx"
	"github.com/team-loco/loco/gen/go/loco/observability/v1/observabilityv1connect"
	"github.com/team-loco/loco/internal/rpc"
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
		mux.Handle(prefix, withRequestID(http.HandlerFunc(unavailable)))
		return
	}
	handler := &httputil.ReverseProxy{
		Transport:     otelhttp.NewTransport(http.DefaultTransport),
		Rewrite:       func(request *httputil.ProxyRequest) { request.SetURL(target); request.SetXForwarded() },
		FlushInterval: observabilityFlushInterval,
		ModifyResponse: func(response *http.Response) error {
			// withRequestID already set the ID the proxy echoes; one copy is enough.
			response.Header.Del(rpc.RequestIDHeader)
			for name := range response.Header {
				if strings.HasPrefix(strings.ToLower(name), "access-control-") {
					response.Header.Del(name)
				}
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			slog.WarnContext(r.Context(), "observability proxy request failed", "error", err)
			unavailable(w, r)
		},
	}
	mux.Handle(prefix, withRequestID(handler))
}

// withRequestID gives forwarded RPCs the request ID the connect interceptor gives the API's own
// RPCs, and forwards it so the proxy logs under the same ID.
func withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rid := rpc.ResolveRequestID(r.Header.Get(rpc.RequestIDHeader))
		r.Header.Set(rpc.RequestIDHeader, rid)
		w.Header().Set(rpc.RequestIDHeader, rid)
		ctx := reqctx.WithRequest(r.Context(), reqctx.Request{ID: rid, Procedure: r.URL.Path})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
