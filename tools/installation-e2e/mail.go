package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"
)

const (
	mailReadHeaderTimeout = 5 * time.Second
	mailBodyLimit         = 1 << 20
)

func mailSink(ctx context.Context) error {
	server := &http.Server{
		Addr:              ":8081",
		ReadHeaderTimeout: mailReadHeaderTimeout,
		Handler:           http.HandlerFunc(mailSinkRequest),
	}
	completed := make(chan error, 1)
	go func() { completed <- server.ListenAndServe() }()
	select {
	case err := <-completed:
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			return errors.Join(err, <-completed)
		}
		err := <-completed
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func mailSinkRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if _, err := io.Copy(io.Discard, http.MaxBytesReader(w, r.Body, mailBodyLimit)); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
