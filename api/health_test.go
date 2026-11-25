package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/team-loco/loco/api/db/dbtest"
	"github.com/team-loco/loco/api/pkg/transit/transittest"
)

const (
	dbCheck  = "postgres"
	keyCheck = "transit"
)

func serveReady(t *testing.T, checks ...readinessCheck) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, readyPath, nil)
	readyHandler(checks...)(rec, req)
	return rec
}

func TestReadyHandlerPasses(t *testing.T) {
	rec := serveReady(t,
		readinessCheck{dbCheck, func(context.Context) error { return nil }},
		readinessCheck{keyCheck, func(context.Context) error { return nil }},
	)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestReadyHandlerNamesFailingDependencyWithoutItsError(t *testing.T) {
	rec := serveReady(t,
		readinessCheck{dbCheck, func(context.Context) error { return nil }},
		readinessCheck{keyCheck, func(context.Context) error {
			return errors.New("dial tcp openbao.internal:8200: refused")
		}},
	)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "transit") || strings.Contains(body, "openbao.internal") {
		t.Fatalf("body = %q, want it to name transit and hide the error", body)
	}
}

func TestReadyHandlerBoundsSlowDependency(t *testing.T) {
	start := time.Now()
	rec := serveReady(t, readinessCheck{dbCheck, func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}})
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if elapsed := time.Since(start); elapsed > 2*readinessTimeout {
		t.Fatalf("took %s, want about %s", elapsed, readinessTimeout)
	}
}

func TestLiveHandlerPasses(t *testing.T) {
	rec := httptest.NewRecorder()
	liveHandler(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, livePath, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestNewReadyHandlerReportsDatabaseAndTransit(t *testing.T) {
	pool := dbtest.NewPool(t)
	fake := transittest.New(t)
	keys, err := newTestTransitKeys(t, fake, transittest.Key, transittest.SigningKey, nil)
	if err != nil {
		t.Fatal(err)
	}
	handler := newReadyHandler(pool, keys.Ingest)
	serve := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		handler(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, readyPath, nil))
		return rec
	}
	if rec := serve(); rec.Code != http.StatusOK {
		t.Fatalf("healthy status = %d, want 200", rec.Code)
	}
	fake.DropSigningKeys()
	if rec := serve(); rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), keyCheck) {
		t.Fatalf("transit down = %d %q, want 503 naming transit", rec.Code, rec.Body.String())
	}
	pool.Close()
	if rec := serve(); rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), dbCheck) {
		t.Fatalf("database down = %d %q, want 503 naming postgres", rec.Code, rec.Body.String())
	}
	rec := httptest.NewRecorder()
	liveHandler(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, livePath, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("live status = %d, want 200", rec.Code)
	}
}

func TestReadyHandlerRecordsNoSpans(t *testing.T) {
	recorder := recordGlobalSpans(t)
	fake := transittest.New(t)
	keys, err := newTestTransitKeys(t, fake, transittest.Key, transittest.SigningKey, nil)
	if err != nil {
		t.Fatal(err)
	}
	recorder.Reset()
	serveReady(t, readinessCheck{keyCheck, func(ctx context.Context) error {
		_, keyErr := keys.Ingest.KeyID(ctx)
		return keyErr
	}})
	if spans := recorder.Ended(); len(spans) != 0 {
		t.Fatalf("spans = %v, want none", spanNames(spans))
	}
}
