package webhooksig_test

import (
	"errors"
	"net/http"
	"testing"
	"time"

	standardwebhooks "github.com/standard-webhooks/standard-webhooks/libraries/go"
	"github.com/team-loco/loco/api/pkg/webhooksig"
)

// The key is 32 bytes of 'k'. Receivers verify these exact bytes, so the expected signature
// was computed independently with openssl and must never change.
const (
	fixedSecret    = "whsec_a2tra2tra2tra2tra2tra2tra2tra2tra2tra2tra2s=" //nolint:gosec // test vector
	fixedSignature = "v1,lMB6gqPBDu9xbq2miSAjth0omJaHYpmpyUb+2+i3z9M="
	fixedBody      = `{"type":"org.updated"}`
)

func TestSignPinsWireFormat(t *testing.T) {
	got, err := webhooksig.Sign(fixedSecret, "msg_1", time.Unix(1700000000, 0), []byte(fixedBody))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if got != fixedSignature {
		t.Fatalf("signature = %q, want %q", got, fixedSignature)
	}
}

func TestSignedRequestVerifies(t *testing.T) {
	secret, err := webhooksig.NewSecret()
	if err != nil {
		t.Fatalf("secret: %v", err)
	}
	req, err := webhooksig.NewSignedRequest(t.Context(), "https://hook.test/x", secret, "msg_1", []byte(fixedBody))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	verifier, err := standardwebhooks.NewWebhook(secret)
	if err != nil {
		t.Fatalf("verifier: %v", err)
	}
	if err := verifier.Verify([]byte(fixedBody), req.Header); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if err := verifier.Verify([]byte(`{"type":"x"}`), req.Header); err == nil {
		t.Fatal("tampered body verified")
	}
	if req.Method != http.MethodPost || req.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("request = %s %v", req.Method, req.Header)
	}
}

func TestMalformedSecretRejected(t *testing.T) {
	for _, secret := range []string{"", "plain", "whsec_not base64", "whsec_YQ=="} {
		if err := webhooksig.ValidateSecret(secret); !errors.Is(err, webhooksig.ErrInvalidSecret) {
			t.Errorf("ValidateSecret(%q) = %v", secret, err)
		}
		if _, err := webhooksig.Sign(secret, "m", time.Now(), nil); !errors.Is(err, webhooksig.ErrInvalidSecret) {
			t.Errorf("Sign(%q) = %v", secret, err)
		}
	}
}
