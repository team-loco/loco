package capacity

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type scaleReader struct{ client.Reader }

func (scaleReader) List(_ context.Context, list client.ObjectList, options ...client.ListOption) error {
	opts := &client.ListOptions{}
	opts.ApplyOptions(options)
	offset := 0
	if opts.Continue != "" {
		parsed, err := strconv.Atoi(opts.Continue)
		if err != nil {
			return err
		}
		offset = parsed
	}
	pods, ok := list.(*corev1.PodList)
	if !ok {
		return errors.New("unexpected list type")
	}
	for i := offset; i < min(offset+int(opts.Limit), 100000); i++ {
		pod := assigned(fmt.Sprintf("pod-%d", i))
		pods.Items = append(pods.Items, *pod)
	}
	if offset+int(opts.Limit) < 100000 {
		pods.Continue = strconv.Itoa(offset + int(opts.Limit))
	}
	return nil
}

type discardResponse struct {
	header http.Header
	bytes  int
}

func (w *discardResponse) Header() http.Header         { return w.header }
func (*discardResponse) WriteHeader(int)               {}
func (w *discardResponse) Write(p []byte) (int, error) { w.bytes += len(p); return len(p), nil }

func BenchmarkHundredThousandPodScrape(b *testing.B) {
	handler := Handler(scaleReader{}, leader())
	b.ReportAllocs()
	for b.Loop() {
		response := &discardResponse{header: make(http.Header)}
		handler.ServeHTTP(response, httptest.NewRequestWithContext(b.Context(), http.MethodGet, "/capacity", nil))
		b.ReportMetric(float64(response.bytes), "response-bytes")
	}
}
