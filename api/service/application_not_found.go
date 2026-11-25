package service

import (
	"embed"
	"html/template"
	"log/slog"
	"net"
	"net/http"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation"
)

//go:embed application_not_found.html application_logo.svg
var applicationNotFoundAssets embed.FS

var applicationNotFoundPage = template.Must(
	template.ParseFS(applicationNotFoundAssets, "application_not_found.html", "application_logo.svg"),
)

func (s *DomainServer) ApplicationNotFound(w http.ResponseWriter, r *http.Request) {
	host := strings.ToLower(r.Header.Get("X-Forwarded-Host"))
	if name, _, err := net.SplitHostPort(host); err == nil {
		host = name
	}
	title := "Application not found"
	message := "No application is available at this address. Please check the URL and try again."
	status := http.StatusNotFound
	ctx := r.Context()
	if len(validation.IsDNS1123Subdomain(host)) == 0 {
		available, err := s.queries.CheckDomainAvailability(ctx, host)
		switch {
		case err != nil:
			slog.ErrorContext(ctx, "look up application domain", "error", err)
			title = "Application unavailable"
			message = "Please try again later."
			status = http.StatusServiceUnavailable
		case !available:
			title = "Application unavailable"
			message = "This application is not currently serving requests. Contact the application owner or try again later."
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; frame-ancestors 'none'")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	if err := applicationNotFoundPage.Execute(w, struct {
		Title, Message string
		Status         int
	}{title, message, status}); err != nil {
		slog.DebugContext(ctx, "write application error page", "error", err)
	}
}
