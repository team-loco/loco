package controller

import (
	"io"
	"net/http"
	"strings"
	"sync"

	. "github.com/onsi/gomega"
)

type pullWatchTransport struct {
	base      http.RoundTripper
	mu        sync.Mutex
	active    map[string]int
	selectors map[string][]string
}

func (t *pullWatchTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if !strings.HasPrefix(req.URL.Path, "/apis/events.k8s.io/v1/") || !strings.HasSuffix(req.URL.Path, "/events") {
		return t.base.RoundTrip(req)
	}
	path := req.URL.Path
	t.mu.Lock()
	t.selectors[path] = append(t.selectors[path], req.URL.Query().Get("fieldSelector"))
	t.mu.Unlock()
	response, err := t.base.RoundTrip(req)
	if err != nil || !req.URL.Query().Has("watch") {
		return response, err
	}
	t.mu.Lock()
	t.active[path]++
	t.mu.Unlock()
	response.Body = &pullWatchBody{ReadCloser: response.Body, close: func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		t.active[path]--
	}}
	return response, nil
}

func (t *pullWatchTransport) watches(namespace string) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.active["/apis/events.k8s.io/v1/namespaces/"+namespace+"/events"]
}

func (t *pullWatchTransport) expectScoped(workspace, outside string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	Expect(t.selectors).To(HaveKey("/apis/events.k8s.io/v1/namespaces/" + workspace + "/events"))
	Expect(t.selectors).NotTo(HaveKey("/apis/events.k8s.io/v1/namespaces/" + outside + "/events"))
	Expect(t.selectors).NotTo(HaveKey("/apis/events.k8s.io/v1/events"))
	for _, selectors := range t.selectors {
		for _, selector := range selectors {
			Expect(selector).To(Equal("regarding.kind=Pod"))
		}
	}
}

type pullWatchBody struct {
	io.ReadCloser
	once  sync.Once
	close func()
}

func (b *pullWatchBody) Close() error {
	err := b.ReadCloser.Close()
	b.once.Do(b.close)
	return err
}
