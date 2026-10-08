package webhooks_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/team-loco/loco/api/webhooks"
)

const (
	maxBody                 = 1 << 20
	standardVectorKey       = "whsec_MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw"
	standardVectorID        = "msg_p5jXN8AQM9LWM0D4loKWxJek"
	standardVectorTimestamp = 1614265330
	standardVectorBody      = `{"test": 2432232314}`
	standardVectorSignature = "v1,g0hM9SsE+OTPJTGt/tmIKtSyZlE3uFJELVlNIOLJ1OE="
)

func TestSignatureVerifiesAsStandardWebhooks(t *testing.T) {
	secret, err := webhooks.NewSecret()
	if err != nil {
		t.Fatalf("secret: %v", err)
	}
	verifier, err := newStandardVerifier(secret)
	if err != nil {
		t.Fatalf("verifier: %v", err)
	}
	body := `{"type":"org.updated"}`
	signature, err := webhooks.Sign(secret, "msg_1", time.Now(), []byte(body))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", strings.NewReader(body))
	req.Header.Set("Webhook-Id", "msg_1")
	req.Header.Set("Webhook-Timestamp", strconv.FormatInt(time.Now().Unix(), 10))
	req.Header.Set("Webhook-Signature", signature)
	got, err := verifier.Verify(req, maxBody)
	if err != nil || string(got) != body {
		t.Fatalf("verify = %q, %v", got, err)
	}

	tampered := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", strings.NewReader(`{"type":"x"}`))
	tampered.Header = req.Header.Clone()
	if _, err := verifier.Verify(tampered, maxBody); err == nil {
		t.Fatal("tampered body verified")
	}
	if _, err := webhooks.Sign(
		"whsec_not base64",
		"msg_1",
		time.Now(),
		nil,
	); !errors.Is(
		err,
		webhooks.ErrInvalidSecret,
	) {
		t.Fatalf("bad secret: %v", err)
	}
}

func TestSignMatchesStandardWebhooksVector(t *testing.T) {
	timestamp := time.Unix(standardVectorTimestamp, 0)
	signature, err := webhooks.Sign(standardVectorKey, standardVectorID, timestamp, []byte(standardVectorBody))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if signature != standardVectorSignature {
		t.Fatalf("signature = %q, want %q", signature, standardVectorSignature)
	}
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
	blockedRes, err := webhooks.NewClient(false).Do(req)
	if err == nil {
		if closeErr := blockedRes.Body.Close(); closeErr != nil {
			t.Fatalf("close: %v", closeErr)
		}
	}
	if !errors.Is(err, webhooks.ErrBlockedAddress) {
		t.Fatalf("loopback request = %v, want ErrBlockedAddress", err)
	}
	res, err := webhooks.NewClient(true).Do(req)
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
