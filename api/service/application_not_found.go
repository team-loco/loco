package service

import (
	_ "embed"
	"html/template"
	"log/slog"
	"net/http"
	"strings"

	"github.com/team-loco/loco/api/pkg/appdomain"
)

//go:embed application_not_found.html
var applicationNotFoundHTML string

var applicationNotFoundPage = template.Must(template.New("application-not-found").Parse(applicationNotFoundHTML))

func (s *DomainServer) ApplicationNotFound(w http.ResponseWriter, r *http.Request) {
	host := strings.ToLower(r.Header.Get("X-Forwarded-Host"))
	title := "Application not found"
	message := "No application is available at this address. Check the URL and try again."
	status := http.StatusNotFound
	ctx := r.Context()
	clusters, err := eligibleClusters(ctx, s.queries, s.silentAfter, s.defaults.TargetPlatform)
	if err == nil {
		if _, _, matchErr := appdomain.Match(host, appdomain.Served(regionClusters(clusters))); matchErr == nil {
			var available bool
			available, err = s.queries.CheckDomainAvailability(ctx, host)
			if err == nil && !available {
				title = "Application unavailable"
				message = "This application is not currently serving requests. Contact the application owner or try again later."
			}
		}
	}
	if err != nil {
		slog.ErrorContext(ctx, "look up application domain", "error", err)
		title = "Application unavailable"
		message = "Please try again later."
		status = http.StatusServiceUnavailable
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
