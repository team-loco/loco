package auth

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	hookPath       = "/auth/hooks/before-user-created"
	devEmail       = "dev@acme.test"
	testHookKey    = "dGhpcyBpcyBhIHRlc3Qgd2ViaG9vayBzZWNyZXQga2V5IQ=="
	testHookSecret = "v1,whsec_" + testHookKey
	otherHookKey   = "YW5vdGhlciB0ZXN0IHdlYmhvb2sgc2VjcmV0IGtleSBoZXJl"
)

func signedRequest(t *testing.T, key string, ts time.Time, body any) *http.Request {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	secret, err := base64.StdEncoding.DecodeString(key)
	if err != nil {
		t.Fatalf("decode key: %v", err)
	}
	id := "msg_1"
	stamp := strconv.FormatInt(ts.Unix(), 10)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(id + "." + stamp + "." + string(raw)))
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, hookPath, bytes.NewReader(raw))
	req.Header.Set("Webhook-Id", id)
	req.Header.Set("Webhook-Timestamp", stamp)
	req.Header.Set("Webhook-Signature", "v1,"+base64.StdEncoding.EncodeToString(mac.Sum(nil)))
	return req
}

type hookFixture struct {
	mux *http.ServeMux
}

func newHookFixture(t *testing.T, mode, domains string) *hookFixture {
	t.Helper()
	webhook, err := ParseWebhookSecrets(testHookSecret + "|v1,whsec_" + otherHookKey)
	if err != nil {
		t.Fatalf("secrets: %v", err)
	}
	policy, err := ParseSignupPolicy(mode, domains)
	if err != nil {
		t.Fatalf("policy: %v", err)
	}
	f := &hookFixture{mux: http.NewServeMux()}
	NewHooks(webhook, policy).Register(f.mux)
	return f
}

type hookResponse struct {
	Error *struct {
		HTTPCode int    `json:"http_code"`
		Message  string `json:"message"`
	} `json:"error"`
}

func (f *hookFixture) do(t *testing.T, req *http.Request) (int, hookResponse) {
	t.Helper()
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	var resp hookResponse
	if rec.Code == http.StatusOK {
		if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
			t.Fatalf("content type = %q", ct)
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode response %q: %v", rec.Body.String(), err)
		}
	}
	return rec.Code, resp
}

func userPayload(email string) map[string]any {
	return map[string]any{"user": map[string]any{claimEmail: email}}
}

func TestBeforeUserCreatedAppliesPolicy(t *testing.T) {
	f := newHookFixture(t, "domains", "acme.test")

	code, resp := f.do(t, signedRequest(t, testHookKey, time.Now(),
		userPayload(devEmail)))
	if code != http.StatusOK || resp.Error != nil {
		t.Fatalf("allowed signup: %d %+v", code, resp.Error)
	}

	code, resp = f.do(t, signedRequest(t, otherHookKey, time.Now(),
		userPayload("Dev@Evil.test")))
	if code != http.StatusOK || resp.Error == nil || resp.Error.HTTPCode != http.StatusForbidden {
		t.Fatalf("rejected signup: %d %+v", code, resp.Error)
	}
	if !strings.Contains(resp.Error.Message, "evil.test") {
		t.Fatalf("message = %q", resp.Error.Message)
	}
}

func TestHooksRejectBadSignatures(t *testing.T) {
	f := newHookFixture(t, "open", "")
	wrongKey := base64.StdEncoding.EncodeToString([]byte("not the configured secret, nope!"))

	unsigned := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		hookPath,
		strings.NewReader("{}"),
	)
	tampered := signedRequest(t, testHookKey, time.Now(), userPayload("a@b.test"))
	tampered.Body = httpBody(`{"user":{"email":"evil@b.test"}}`)

	for name, req := range map[string]*http.Request{
		"unsigned":  unsigned,
		"wrong key": signedRequest(t, wrongKey, time.Now(), userPayload("a@b.test")),
		"stale": signedRequest(t, testHookKey,
			time.Now().Add(-10*time.Minute), userPayload("a@b.test")),
		"future": signedRequest(t, testHookKey,
			time.Now().Add(10*time.Minute), userPayload("a@b.test")),
		"tampered": tampered,
	} {
		t.Run(name, func(t *testing.T) {
			if code, _ := f.do(t, req); code != http.StatusUnauthorized {
				t.Fatalf("code = %d, want 401", code)
			}
		})
	}
}

func httpBody(s string) *readCloser {
	return &readCloser{Reader: strings.NewReader(s)}
}

type readCloser struct {
	*strings.Reader
}

func (*readCloser) Close() error { return nil }

func TestParseWebhookSecrets(t *testing.T) {
	for name, raw := range map[string]string{
		"empty":      "",
		"no prefix":  testHookKey,
		"not base64": "v1,whsec_%%%",
		"too short":  "v1,whsec_" + base64.StdEncoding.EncodeToString([]byte("short")),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseWebhookSecrets(raw); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}
