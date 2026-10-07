package auth

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/team-loco/loco/api/notify"
)

const (
	devEmail       = "dev@acme.test"
	tokenHashKey   = "token_hash"
	testHookKey    = "dGhpcyBpcyBhIHRlc3Qgd2ViaG9vayBzZWNyZXQga2V5IQ=="
	testHookSecret = "v1,whsec_" + testHookKey
	otherHookKey   = "YW5vdGhlciB0ZXN0IHdlYmhvb2sgc2VjcmV0IGtleSBoZXJl"
)

type fakeMailer struct {
	mu   sync.Mutex
	sent []notify.Message
	err  error
}

func (m *fakeMailer) Send(_ context.Context, msg notify.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	m.sent = append(m.sent, msg)
	return nil
}

func signedRequest(t *testing.T, path, key string, ts time.Time, body any) *http.Request {
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
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, path, bytes.NewReader(raw))
	req.Header.Set("Webhook-Id", id)
	req.Header.Set("Webhook-Timestamp", stamp)
	req.Header.Set("Webhook-Signature", "v1,"+base64.StdEncoding.EncodeToString(mac.Sum(nil)))
	return req
}

type hookFixture struct {
	mux    *http.ServeMux
	mailer *fakeMailer
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
	f := &hookFixture{mux: http.NewServeMux(), mailer: &fakeMailer{}}
	NewHooks(webhook, policy, f.mailer, "https://app.loco.test/").Register(f.mux)
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

	code, resp := f.do(t, signedRequest(t, "/auth/hooks/before-user-created", testHookKey, time.Now(),
		userPayload(devEmail)))
	if code != http.StatusOK || resp.Error != nil {
		t.Fatalf("allowed signup: %d %+v", code, resp.Error)
	}

	code, resp = f.do(t, signedRequest(t, "/auth/hooks/before-user-created", otherHookKey, time.Now(),
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
		"/auth/hooks/send-email",
		strings.NewReader("{}"),
	)
	tampered := signedRequest(t, "/auth/hooks/send-email", testHookKey, time.Now(), userPayload("a@b.test"))
	tampered.Body = httpBody(`{"user":{"email":"evil@b.test"}}`)

	for name, req := range map[string]*http.Request{
		"unsigned":  unsigned,
		"wrong key": signedRequest(t, "/auth/hooks/send-email", wrongKey, time.Now(), userPayload("a@b.test")),
		"stale": signedRequest(t, "/auth/hooks/send-email", testHookKey,
			time.Now().Add(-10*time.Minute), userPayload("a@b.test")),
		"future": signedRequest(t, "/auth/hooks/send-email", testHookKey,
			time.Now().Add(10*time.Minute), userPayload("a@b.test")),
		"tampered": tampered,
	} {
		t.Run(name, func(t *testing.T) {
			if code, _ := f.do(t, req); code != http.StatusUnauthorized {
				t.Fatalf("code = %d, want 401", code)
			}
		})
	}
	if len(f.mailer.sent) != 0 {
		t.Fatalf("mail sent for rejected hooks: %d", len(f.mailer.sent))
	}
}

func httpBody(s string) *readCloser {
	return &readCloser{Reader: strings.NewReader(s)}
}

type readCloser struct {
	*strings.Reader
}

func (*readCloser) Close() error { return nil }

func confirmLink(t *testing.T, msg notify.Message) url.Values {
	t.Helper()
	i := strings.Index(msg.Text, "https://app.loco.test/auth/confirm?")
	if i < 0 {
		t.Fatalf("no confirm link in %q", msg.Text)
	}
	raw := strings.Fields(msg.Text[i:])[0]
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse link: %v", err)
	}
	if !strings.Contains(msg.HTML, strings.ReplaceAll(raw, "&", "&amp;")) {
		t.Fatalf("html body lacks the link %q", raw)
	}
	return u.Query()
}

func emailRequest(t *testing.T, action string, user map[string]any, data map[string]any) *http.Request {
	t.Helper()
	data["email_action_type"] = action
	return signedRequest(t, "/auth/hooks/send-email", testHookKey, time.Now(), map[string]any{
		"user":       user,
		"email_data": data,
	})
}

func TestSendEmailBuildsConfirmLinks(t *testing.T) {
	for action, verifyType := range map[string]string{
		"signup":    "signup",
		"magiclink": "magiclink",
		"recovery":  "recovery",
		"invite":    "invite",
	} {
		t.Run(action, func(t *testing.T) {
			f := newHookFixture(t, "open", "")
			code, resp := f.do(t, emailRequest(t, action, map[string]any{claimEmail: devEmail}, map[string]any{
				tokenHashKey:  "hash-123",
				"redirect_to": "https://app.loco.test/dashboard",
			}))
			if code != http.StatusOK || resp.Error != nil {
				t.Fatalf("hook: %d %+v", code, resp.Error)
			}
			if len(f.mailer.sent) != 1 {
				t.Fatalf("sent %d messages", len(f.mailer.sent))
			}
			msg := f.mailer.sent[0]
			if msg.To != devEmail || msg.Subject == "" {
				t.Fatalf("message = %+v", msg)
			}
			q := confirmLink(t, msg)
			if q.Get(tokenHashKey) != "hash-123" || q.Get("type") != verifyType ||
				q.Get("redirect_to") != "https://app.loco.test/dashboard" {
				t.Fatalf("link query = %v", q)
			}
		})
	}
}

func TestSendEmailCodes(t *testing.T) {
	for _, action := range []string{actionEmailOTP, "reauthentication"} {
		t.Run(action, func(t *testing.T) {
			f := newHookFixture(t, "open", "")
			code, resp := f.do(t, emailRequest(t, action, map[string]any{claimEmail: devEmail}, map[string]any{
				"token": "123456",
			}))
			if code != http.StatusOK || resp.Error != nil {
				t.Fatalf("hook: %d %+v", code, resp.Error)
			}
			msg := f.mailer.sent[0]
			if !strings.Contains(msg.Text, "123456") || !strings.Contains(msg.HTML, "123456") {
				t.Fatalf("code missing from %+v", msg)
			}
		})
	}
}

func TestSendEmailChangeNotifiesBothAddresses(t *testing.T) {
	f := newHookFixture(t, "open", "")
	code, resp := f.do(t, emailRequest(t, "email_change",
		map[string]any{claimEmail: "old@acme.test", "new_email": "new@acme.test"},
		map[string]any{tokenHashKey: "hash-new-address", "token_hash_new": "hash-current-address"},
	))
	if code != http.StatusOK || resp.Error != nil {
		t.Fatalf("hook: %d %+v", code, resp.Error)
	}
	if len(f.mailer.sent) != 2 {
		t.Fatalf("sent %d messages", len(f.mailer.sent))
	}
	byRecipient := map[string]url.Values{}
	for _, msg := range f.mailer.sent {
		byRecipient[msg.To] = confirmLink(t, msg)
	}
	if byRecipient["new@acme.test"].Get(tokenHashKey) != "hash-new-address" {
		t.Fatalf("new address link = %v", byRecipient["new@acme.test"])
	}
	if byRecipient["old@acme.test"].Get(tokenHashKey) != "hash-current-address" {
		t.Fatalf("current address link = %v", byRecipient["old@acme.test"])
	}
}

func TestSendEmailNotificationsAndUnknownActions(t *testing.T) {
	f := newHookFixture(t, "open", "")
	code, resp := f.do(t, emailRequest(t, "password_changed_notification", map[string]any{claimEmail: devEmail},
		map[string]any{}))
	if code != http.StatusOK || resp.Error != nil || len(f.mailer.sent) != 1 {
		t.Fatalf("notification: %d %+v sent=%d", code, resp.Error, len(f.mailer.sent))
	}

	code, resp = f.do(t, emailRequest(t, "something_new", map[string]any{claimEmail: devEmail}, map[string]any{}))
	if code != http.StatusOK || resp.Error == nil || resp.Error.HTTPCode != http.StatusInternalServerError {
		t.Fatalf("unknown action: %d %+v", code, resp.Error)
	}
}

func TestSendEmailReportsMailerFailure(t *testing.T) {
	f := newHookFixture(t, "open", "")
	f.mailer.err = errors.New("smtp down")
	code, resp := f.do(t, emailRequest(t, "signup", map[string]any{claimEmail: devEmail},
		map[string]any{tokenHashKey: "h"}))
	if code != http.StatusOK || resp.Error == nil || resp.Error.HTTPCode != http.StatusInternalServerError {
		t.Fatalf("mailer failure: %d %+v", code, resp.Error)
	}
}

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
