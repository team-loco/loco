package webhooks_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"

	"github.com/team-loco/loco/api/webhooks"
)

var testConfig = webhooks.Config{
	RequestTimeout: 10 * time.Second,
	ClaimBatch:     50,
	RetryDelays: []time.Duration{
		30 * time.Second,
		2 * time.Minute,
		10 * time.Minute,
		time.Hour,
		6 * time.Hour,
		24 * time.Hour,
	},
	PollInterval: time.Second,
}

func TestClientRefusesPrivateNetworks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL, http.NoBody)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	blockedRes, err := webhooks.NewClient(testConfig, false).Do(req)
	if err == nil {
		if closeErr := blockedRes.Body.Close(); closeErr != nil {
			t.Fatalf("close: %v", closeErr)
		}
	}
	if !errors.Is(err, webhooks.ErrBlockedAddress) {
		t.Fatalf("loopback request = %v, want ErrBlockedAddress", err)
	}
	res, err := webhooks.NewClient(testConfig, true).Do(req)
	if err != nil {
		t.Fatalf("allowed request: %v", err)
	}
	if closeErr := res.Body.Close(); closeErr != nil {
		t.Fatalf("close: %v", closeErr)
	}
}

func TestBlockedAddresses(t *testing.T) {
	for addr, want := range map[string]bool{
		"127.0.0.1":       true,
		"10.1.2.3":        true,
		"172.16.0.9":      true,
		"192.168.1.1":     true,
		"169.254.169.254": true,
		"100.64.0.1":      true,
		"0.0.0.0":         true,
		"::1":             true,
		"fd00::1":         true,
		"fe80::1":         true,
		"::ffff:10.0.0.1": true,
		"8.8.8.8":         false,
		"2606:4700::1111": false,
	} {
		if got := webhooks.Blocked(netip.MustParseAddr(addr)); got != want {
			t.Errorf("%s blocked = %v, want %v", addr, got, want)
		}
	}
}

func TestLeaseCoversWorstCaseBatch(t *testing.T) {
	worst := time.Duration(testConfig.ClaimBatch) * testConfig.RequestTimeout
	if lease := webhooks.Lease(testConfig); lease < worst {
		t.Fatalf("lease %s is shorter than the worst-case batch %s", lease, worst)
	}
}
