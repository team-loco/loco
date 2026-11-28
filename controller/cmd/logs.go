package main

import (
	"io"
	"log/slog"

	"github.com/go-logr/logr"
	"github.com/team-loco/loco/internal/otelsetup"
	ctrl "sigs.k8s.io/controller-runtime"
)

func configureLogging(output io.Writer, level slog.Leveler) {
	handler := otelsetup.LogHandler(slog.NewJSONHandler(output, &slog.HandlerOptions{Level: level}), nil)
	slog.SetDefault(slog.New(handler))
	ctrl.SetLogger(logr.FromSlogHandler(handler))
}
