package platform

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
)

// ntfyTopic is the topic the API publishes to.
const ntfyTopic = "e2e-alerts"

// NtfyMessage is one notification the API published.
type NtfyMessage struct {
	Title    string
	Message  string
	Priority string
	Click    string
}

// Ntfy is the ntfy server the API publishes to.
type Ntfy struct {
	srv      *httptest.Server
	mu       sync.Mutex
	messages []NtfyMessage
}

func startNtfy() *Ntfy {
	n := &Ntfy{}
	n.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		query := r.URL.Query()
		n.mu.Lock()
		n.messages = append(n.messages, NtfyMessage{
			Title: query.Get(
				"title",
			),
			Message:  string(body),
			Priority: query.Get("priority"),
			Click:    query.Get("click"),
		})
		n.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	return n
}

// Messages returns the notifications published so far.
func (n *Ntfy) Messages() []NtfyMessage {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]NtfyMessage(nil), n.messages...)
}
