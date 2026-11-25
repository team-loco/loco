package platform

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
)

// installWebhookSecret signs the deliveries to the install webhook: whsec_ and the base64 of 32 zero bytes.
const installWebhookSecret = "whsec_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="

// WebhookEvent is one event delivered to the install webhook.
type WebhookEvent struct {
	Type    string          `json:"type"`
	Subject WebhookSubject  `json:"subject"`
	Data    json.RawMessage `json:"data"`
}

// WebhookSubject is the object an event is about.
type WebhookSubject struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// WebhookReceiver is the server the API's install webhook posts to.
type WebhookReceiver struct {
	srv    *httptest.Server
	mu     sync.Mutex
	events []WebhookEvent
}

func startWebhookReceiver() *WebhookReceiver {
	r := &WebhookReceiver{}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var event WebhookEvent
		if err = json.Unmarshal(body, &event); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		r.mu.Lock()
		r.events = append(r.events, event)
		r.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	return r
}

// Events returns the events delivered so far.
func (r *WebhookReceiver) Events() []WebhookEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]WebhookEvent(nil), r.events...)
}
