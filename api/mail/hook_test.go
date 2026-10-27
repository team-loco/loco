package mail

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func testEvent() InvitationEvent {
	return InvitationEvent{
		Invitation: InvitationInfo{
			ID: uuid.New(), OrgID: uuid.New(), Email: "bob@acme.test",
			ExpiresAt: time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC),
		},
		Org:     OrgInfo{ID: uuid.New(), Name: "Acme"},
		Inviter: InviterInfo{ID: uuid.New(), Email: "alice@acme.test", Name: "Alice"},
		Link:    "https://app.loco.test/login?login_hint=bob%40acme.test",
	}
}

func newTestHook(t *testing.T, handler http.HandlerFunc) Transport {
	t.Helper()
	srv := httptest.NewTLSServer(handler)
	t.Cleanup(srv.Close)
	transport, err := ParseTransport(srv.URL, testFrom, testHookSecret())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	hook, ok := transport.(*hookTransport)
	if !ok {
		t.Fatalf("transport is %T", transport)
	}
	hook.client = srv.Client()
	return hook
}

func TestHookPostsSignedInvitation(t *testing.T) {
	event := testEvent()
	var gotBody []byte
	var gotHeader http.Header
	hook := newTestHook(t, func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Clone()
		var readErr error
		gotBody, readErr = io.ReadAll(r.Body)
		if readErr != nil {
			t.Errorf("read body: %v", readErr)
		}
		w.WriteHeader(http.StatusNoContent)
	})

	if err := hook.Send(t.Context(), NewInvitationMessage(event)); err != nil {
		t.Fatalf("send: %v", err)
	}

	id := gotHeader.Get("Webhook-Id")
	timestamp := gotHeader.Get("Webhook-Timestamp")
	seconds, err := strconv.ParseInt(timestamp, 10, 64)
	if id == "" || err != nil || time.Since(time.Unix(seconds, 0)).Abs() > time.Minute {
		t.Fatalf("id = %q timestamp = %q", id, timestamp)
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(testHookSecret(), "whsec_"))
	if err != nil {
		t.Fatalf("decode secret: %v", err)
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(id + "." + timestamp + "."))
	mac.Write(gotBody)
	want := "v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil))
	if got := gotHeader.Get("Webhook-Signature"); got != want {
		t.Fatalf("signature = %q, want %q", got, want)
	}

	var payload map[string]map[string]any
	var raw map[string]any
	if err = json.Unmarshal(gotBody, &raw); err != nil {
		t.Fatalf("body: %v", err)
	}
	if raw["link"] != event.Link {
		t.Fatalf("link = %v", raw["link"])
	}
	delete(raw, "link")
	encoded, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if err = json.Unmarshal(encoded, &payload); err != nil {
		t.Fatalf("shape: %v", err)
	}
	checks := map[string]map[string]any{
		"invitation": {
			"id": event.Invitation.ID.String(), "org_id": event.Invitation.OrgID.String(),
			"email": "bob@acme.test", "expires_at": "2030-01-02T03:04:05Z",
		},
		"org":     {"id": event.Org.ID.String(), "name": "Acme"},
		"inviter": {"id": event.Inviter.ID.String(), "email": "alice@acme.test", "name": "Alice"},
	}
	if len(payload) != len(checks) {
		t.Fatalf("payload keys = %v", payload)
	}
	for section, fields := range checks {
		if len(payload[section]) != len(fields) {
			t.Fatalf("%s = %v, want %v", section, payload[section], fields)
		}
		for name, value := range fields {
			if payload[section][name] != value {
				t.Fatalf("%s.%s = %v, want %v", section, name, payload[section][name], value)
			}
		}
	}
}

func TestHookNon2xxIsAnError(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusInternalServerError, http.StatusMovedPermanently} {
		hook := newTestHook(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
		})
		if err := hook.Send(t.Context(), NewInvitationMessage(testEvent())); err == nil {
			t.Fatalf("status %d delivered", status)
		}
	}
}

func TestHookSurfacesReceiverError(t *testing.T) {
	const message = "bob@x.com already has an account"
	hook := newTestHook(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		if _, err := io.WriteString(w, `{"error":"`+message+`"}`); err != nil {
			t.Errorf("write: %v", err)
		}
	})

	err := hook.Send(t.Context(), NewInvitationMessage(testEvent()))

	if err == nil || err.Error() != message {
		t.Fatalf("error = %v, want %q", err, message)
	}
}

func TestHookFallsBackToStatusWithoutReceiverError(t *testing.T) {
	hook := newTestHook(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		if _, err := io.WriteString(w, "upstream exploded"); err != nil {
			t.Errorf("write: %v", err)
		}
	})

	err := hook.Send(t.Context(), NewInvitationMessage(testEvent()))

	if err == nil || err.Error() != "hook returned 500 Internal Server Error" {
		t.Fatalf("error = %v", err)
	}
}

func TestHookIgnoresBodyOn2xx(t *testing.T) {
	hook := newTestHook(t, func(w http.ResponseWriter, _ *http.Request) {
		if _, err := io.WriteString(w, `{"error":"ignored"}`); err != nil {
			t.Errorf("write: %v", err)
		}
	})

	if err := hook.Send(t.Context(), NewInvitationMessage(testEvent())); err != nil {
		t.Fatalf("send: %v", err)
	}
}
