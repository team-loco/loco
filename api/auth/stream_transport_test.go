package auth

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/team-loco/loco/api/reqctx"
)

type blockedStreamWriter struct {
	header  http.Header
	writing chan struct{}
	expired chan struct{}
	once    sync.Once
}

func (w *blockedStreamWriter) Header() http.Header { return w.header }
func (*blockedStreamWriter) WriteHeader(int)       {}
func (w *blockedStreamWriter) Write([]byte) (int, error) {
	close(w.writing)
	<-w.expired
	return 0, context.DeadlineExceeded
}
func (w *blockedStreamWriter) SetWriteDeadline(deadline time.Time) error {
	if deadline.IsZero() {
		return nil
	}
	if delay := time.Until(deadline); delay > 0 {
		time.AfterFunc(delay, func() { w.once.Do(func() { close(w.expired) }) })
		return nil
	}
	w.once.Do(func() { close(w.expired) })
	return nil
}

func TestStreamRevocationInterruptsBlockedHTTPWrite(t *testing.T) {
	t.Parallel()
	var revoked atomic.Bool
	denied := errors.New("revoked")
	w := &blockedStreamWriter{header: make(http.Header), writing: make(chan struct{}), expired: make(chan struct{})}
	finished := make(chan error, 1)
	handler := StreamTransport(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := WithStreamValidation(
			r.Context(),
			StreamConfig{Interval: time.Millisecond, Timeout: time.Millisecond},
			func(context.Context) (reqctx.Principal, error) {
				if revoked.Load() {
					return reqctx.Principal{}, denied
				}
				return reqctx.Principal{}, nil
			},
		)
		_, stop, err := GuardStream(ctx, func(context.Context) error { return nil })
		if err != nil {
			finished <- err
			return
		}
		_, writeErr := w.Write([]byte("data"))
		failure := stop()
		if writeErr == nil {
			failure = errors.New("blocked write unexpectedly succeeded")
		}
		finished <- failure
	}))
	go handler.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))
	select {
	case <-w.writing:
	case <-time.After(time.Second):
		t.Fatal("stream did not start writing")
	}
	revoked.Store(true)
	select {
	case err := <-finished:
		if !errors.Is(err, denied) {
			t.Fatalf("stream error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("revocation left handler blocked in HTTP write")
	}
}

func TestStreamRevocationClosesBlockedSocket(t *testing.T) {
	t.Parallel()
	var revoked atomic.Bool
	writing := make(chan struct{})
	finished := make(chan error, 1)
	denied := errors.New("revoked")
	server := httptest.NewServer(StreamTransport(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := WithStreamValidation(
			r.Context(),
			StreamConfig{Interval: time.Millisecond, Timeout: 10 * time.Millisecond},
			func(context.Context) (reqctx.Principal, error) {
				if revoked.Load() {
					return reqctx.Principal{}, denied
				}
				return reqctx.Principal{}, nil
			},
		)
		_, stop, err := GuardStream(ctx, func(context.Context) error { return nil })
		if err != nil {
			finished <- err
			return
		}
		close(writing)
		_, writeErr := w.Write(make([]byte, 32<<20))
		failure := stop()
		if writeErr == nil {
			failure = errors.New("blocked write unexpectedly succeeded")
		}
		finished <- failure
	})))
	defer server.Close()
	connection, err := (&net.Dialer{Timeout: time.Second}).DialContext(
		t.Context(),
		"tcp",
		strings.TrimPrefix(server.URL, "http://"),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if _, err = io.WriteString(connection, "GET / HTTP/1.1\r\nHost: stream.test\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-writing:
	case <-time.After(time.Second):
		t.Fatal("handler did not start")
	}
	revoked.Store(true)
	select {
	case err = <-finished:
		if !errors.Is(err, denied) {
			t.Fatalf("stream error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("nonreading peer left the stream handler blocked")
	}
}

func TestStreamRevocationDeadlineIsScopedToHTTP2Stream(t *testing.T) {
	t.Parallel()
	var revoked atomic.Bool
	writing := make(chan struct{})
	finished := make(chan error, 1)
	denied := errors.New("revoked")
	server := httptest.NewUnstartedServer(
		StreamTransport(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/healthy" {
				if _, writeErr := io.WriteString(w, "healthy"); writeErr != nil {
					finished <- writeErr
				}
				return
			}
			ctx := WithStreamValidation(
				r.Context(),
				StreamConfig{Interval: time.Millisecond, Timeout: 10 * time.Millisecond},
				func(context.Context) (reqctx.Principal, error) {
					if revoked.Load() {
						return reqctx.Principal{}, denied
					}
					return reqctx.Principal{}, nil
				},
			)
			_, stop, err := GuardStream(ctx, func(context.Context) error { return nil })
			if err != nil {
				finished <- err
				return
			}
			w.WriteHeader(http.StatusOK)
			if err = http.NewResponseController(w).Flush(); err != nil {
				finished <- errors.Join(err, stop())
				return
			}
			close(writing)
			_, writeErr := w.Write(make([]byte, 32<<20))
			failure := stop()
			if writeErr == nil {
				failure = errors.New("blocked write unexpectedly succeeded")
			}
			finished <- failure
		})),
	)
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.ProtoMajor != 2 {
		t.Fatalf("protocol = %s, want HTTP/2", response.Proto)
	}
	<-writing
	revoked.Store(true)
	select {
	case err = <-finished:
		if !errors.Is(err, denied) {
			t.Fatalf("stream error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("HTTP/2 nonreading peer left handler blocked")
	}
	request, err = http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+"/healthy", nil)
	if err != nil {
		t.Fatal(err)
	}
	healthy, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer healthy.Body.Close()
	body, err := io.ReadAll(healthy.Body)
	if err != nil || string(body) != "healthy" {
		t.Fatalf("other stream body = %q, error = %v", body, err)
	}
}
