package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

const testHostname = "web.apps.example.test"
const testNYTarget = "infra-ny.apps.example.test"
const testBothTarget = "infra-both.apps.example.test"
const testTarget = "infra-ca.apps.example.test"
const testPublicationID = "00000000-0000-7000-8000-000000000001"
const testPublisherID = "00000000-0000-7000-8000-000000000002"
const testCA = "00000000-0000-7000-8000-000000000003"
const testNY = "00000000-0000-7000-8000-000000000004"
const testProviderToken = "synthetic-provider-token"

type dnsResponse struct {
	Success bool `json:"success"`
	Result  any  `json:"result"`
}

type dnsFixture struct {
	records                            []record
	writes, reads                      int
	loseWrite, corruptWrite, rateLimit bool
}

func newDNSFixture(t *testing.T) (*dnsFixture, *dnsClient) {
	t.Helper()
	f := &dnsFixture{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+testProviderToken {
			t.Errorf("authorization=%q", got)
		}
		if f.rateLimit {
			w.Header().Set("Retry-After", "30")
			w.WriteHeader(http.StatusTooManyRequests)
			if _, err := w.Write([]byte("private-provider-detail")); err != nil {
				t.Error(err)
			}
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			f.reads++
			if r.URL.Query().Get("name.exact") != testHostname {
				t.Errorf("name lookup=%q", r.URL.RawQuery)
			}
			page, err := strconv.Atoi(r.URL.Query().Get("page"))
			if err != nil {
				t.Error(err)
				return
			}
			start := (page - 1) * dnsPageSize
			end := min(start+dnsPageSize, len(f.records))
			start = min(start, len(f.records))
			if err := json.NewEncoder(w).
				Encode(dnsResponse{Success: true, Result: f.records[start:end]}); err != nil {
				t.Error(err)
			}
		case http.MethodPost, http.MethodPatch:
			f.writes++
			var value record
			if err := json.NewDecoder(r.Body).Decode(&value); err != nil {
				t.Error(err)
				return
			}
			value.ID = "synthetic-record"
			if f.corruptWrite {
				value.Proxied = false
			}
			f.records = []record{value}
			if f.loseWrite {
				f.loseWrite = false
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			if err := json.NewEncoder(w).Encode(dnsResponse{Success: true, Result: value}); err != nil {
				t.Error(err)
			}
		case http.MethodDelete:
			f.writes++
			f.records = nil
			if err := json.NewEncoder(w).
				Encode(dnsResponse{Success: true, Result: map[string]string{"id": "synthetic-record"}}); err != nil {
				t.Error(err)
			}
		default:
			t.Errorf("unexpected method %s", r.Method)
		}
	}))
	t.Cleanup(server.Close)
	return f, &dnsClient{
		client:  server.Client(),
		baseURL: server.URL,
		token:   testProviderToken,
		zoneID:  strings.Repeat("a", 32),
	}
}
func TestDNSPublicationCreatesReadsBackAndWithdrawsOwnedRecord(t *testing.T) {
	f, d := newDNSFixture(t)
	if err := d.reconcile(t.Context(), testHostname, testTarget, testPublicationID, false); err != nil {
		t.Fatal(err)
	}
	if f.writes != 1 || f.reads != 2 {
		t.Fatalf("writes=%d reads=%d", f.writes, f.reads)
	}
	actual := f.records[0]
	if actual.Type != "CNAME" || actual.Content != testTarget || !actual.Proxied || actual.TTL != 1 ||
		actual.Comment != "loco-managed:"+testPublicationID {
		t.Fatalf("record=%+v", actual)
	}
	if err := d.reconcile(t.Context(), testHostname, testTarget, testPublicationID, false); err != nil {
		t.Fatal(err)
	}
	if f.writes != 1 {
		t.Fatal("matching readback was not idempotent")
	}
	if err := d.reconcile(t.Context(), testHostname, "", testPublicationID, true); err != nil {
		t.Fatal(err)
	}
	if len(f.records) != 0 || f.writes != 2 {
		t.Fatal("withdrawal failed")
	}
	if err := d.reconcile(t.Context(), testHostname, "", testPublicationID, true); err != nil {
		t.Fatal(err)
	}
	if f.writes != 2 {
		t.Fatal("withdrawal was not idempotent")
	}
}
func TestDNSPublicationRecoversLostCreateAndRepairsDrift(t *testing.T) {
	f, d := newDNSFixture(t)
	f.loseWrite = true
	if err := d.reconcile(t.Context(), testHostname, testTarget, testPublicationID, false); err == nil {
		t.Fatal("lost response succeeded")
	}
	if err := d.reconcile(t.Context(), testHostname, testTarget, testPublicationID, false); err != nil {
		t.Fatal(err)
	}
	if f.writes != 1 {
		t.Fatal("lost create duplicated the record")
	}
	f.records[0].Content = testNYTarget
	if err := d.reconcile(t.Context(), testHostname, testTarget, testPublicationID, false); err != nil {
		t.Fatal(err)
	}
	if f.writes != 2 || f.records[0].Content != testTarget {
		t.Fatal("owned drift not repaired")
	}
}
func TestDNSPublicationRejectsUnownedRecordsAndReadbackMismatch(t *testing.T) {
	for _, withdrawing := range []bool{false, true} {
		f, d := newDNSFixture(t)
		f.records = []record{{Type: "A", Name: testHostname, Content: "192.0.2.1"}}
		if err := d.reconcile(t.Context(), testHostname, testTarget, testPublicationID, withdrawing); err == nil {
			t.Fatal("unowned record accepted")
		}
		if f.writes != 0 {
			t.Fatal("unowned record changed")
		}
	}
	f, d := newDNSFixture(t)
	f.corruptWrite = true
	if err := d.reconcile(t.Context(), testHostname, testTarget, testPublicationID, false); err == nil {
		t.Fatal("mismatched readback accepted")
	}
}
func TestDNSPublicationReadsAllPagesAndHonorsRateLimit(t *testing.T) {
	f, d := newDNSFixture(t)
	for range dnsPageSize + 1 {
		f.records = append(f.records, record{Name: testHostname, Comment: "loco-managed:" + testPublicationID})
	}
	if err := d.reconcile(t.Context(), testHostname, testTarget, testPublicationID, false); err == nil {
		t.Fatal("duplicate owned records accepted")
	}
	if f.reads != 2 || f.writes != 0 {
		t.Fatalf("reads=%d writes=%d", f.reads, f.writes)
	}
	f.rateLimit = true
	err := d.reconcile(context.Background(), testHostname, testTarget, testPublicationID, false)
	var limited *providerError
	if !errors.As(err, &limited) || limited.status != http.StatusTooManyRequests ||
		time.Until(limited.retryAt) < 29*time.Second {
		t.Fatalf("rate limit=%v", err)
	}
	if strings.Contains(err.Error(), "private-provider-detail") || strings.Contains(err.Error(), testProviderToken) {
		t.Fatal("unsafe provider diagnostics")
	}
}
