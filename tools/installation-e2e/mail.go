package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"
)

func mailSink(ctx context.Context) error {
	server := &http.Server{
		Addr:              ":8081",
		ReadHeaderTimeout: 5 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			if _, err := io.Copy(io.Discard, http.MaxBytesReader(w, r.Body, 1<<20)); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		}),
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
