package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/go-logr/logr"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/team-loco/loco/internal/otelsetup"
)

func configureLogging(
	ctx context.Context,
	name, version string,
	level slog.Leveler,
	enabled bool,
) (*sdklog.LoggerProvider, error) {
	var provider *sdklog.LoggerProvider
	if enabled {
		var err error
		provider, err = otelsetup.StartLogs(ctx, name, version)
		if err != nil {
			return nil, err
		}
	}
	output := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})
	handler := otelsetup.LogHandler(output, provider, name, level)
	slog.SetDefault(slog.New(handler))
	// controller-runtime fulfills existing loggers only once, after both outputs are ready.
	ctrl.SetLogger(logr.FromSlogHandler(handler))
	return provider, nil
}
