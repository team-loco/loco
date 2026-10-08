package main

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/team-loco/loco/observability-proxy/pkg/config"
)

const (
	startupDeadline   = 2 * time.Second
	healthzDeadline   = 2 * time.Second
	testTokenTTL      = time.Second
	testMaxConcurrent = 1
)

func silentListener(t *testing.T) net.Listener {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	var (
		mu    sync.Mutex
		conns []net.Conn
		wg    sync.WaitGroup
	)
	wg.Go(func() {
		for {
			conn, acceptErr := ln.Accept()
			if acceptErr != nil {
				return
			}
			mu.Lock()
			conns = append(conns, conn)
			mu.Unlock()
		}
	})
	t.Cleanup(func() {
		if err := ln.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			t.Errorf("close listener: %v", err)
		}
		wg.Wait()
		mu.Lock()
		defer mu.Unlock()
		for _, conn := range conns {
			if err := conn.Close(); err != nil {
				t.Errorf("close conn: %v", err)
			}
		}
	})
	return ln
}

func TestHealthzServedWhenClickHouseNeverAnswers(t *testing.T) {
	ln := silentListener(t)
	addr := ln.Addr().String()
	cfg := &config.Config{
		ControlPlaneURL: "http://127.0.0.1:1",
		ClickHouseURL:   "clickhouse://" + addr,
		ClickHouseDB:    "default",
		MaxConcurrent:   testMaxConcurrent,
		TokenCacheTTL:   testTokenTTL,
	}

	started := make(chan *proxy, 1)
	startErr := make(chan error, 1)
	go func() {
		p, err := newProxy(cfg)
		if err != nil {
			startErr <- err
			return
		}
		started <- p
	}()

	var p *proxy
	select {
	case p = <-started:
	case err := <-startErr:
		t.Fatalf("newProxy: %v", err)
	case <-time.After(startupDeadline):
		t.Fatalf("proxy did not start within %s while clickhouse never answered", startupDeadline)
	}
	t.Cleanup(p.close)

	srv := httptest.NewServer(p.server.Handler)
	t.Cleanup(srv.Close)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+"/healthz", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	client := &http.Client{Timeout: healthzDeadline}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Errorf("close body: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /healthz status = %d, want %d", resp.StatusCode, http.StatusOK)
	}
}
