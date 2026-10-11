package testenv

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"sync/atomic"
	"testing"

	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

const assetsEnv = "KUBEBUILDER_ASSETS"

var (
	ErrDisconnected = errors.New("connection to the API server was cut")

	server *rest.Config
)

func Main(m *testing.M) int {
	if os.Getenv(assetsEnv) == "" {
		return m.Run()
	}
	env := &envtest.Environment{}
	cfg, err := env.Start()
	if err != nil {
		fmt.Fprintf(os.Stderr, "start envtest: %v\n", err)
		return 1
	}
	server = cfg
	code := m.Run()
	if err := env.Stop(); err != nil {
		fmt.Fprintf(os.Stderr, "stop envtest: %v\n", err)
		return 1
	}
	return code
}

func APIServer(tb testing.TB) *rest.Config {
	tb.Helper()
	if server == nil {
		tb.Skip(assetsEnv + " not set")
	}
	return rest.CopyConfig(server)
}

type Switch struct {
	cut atomic.Bool
}

func (s *Switch) Cut() {
	s.cut.Store(true)
}

func (s *Switch) Wrap(cfg *rest.Config) *rest.Config {
	cfg.Wrap(func(next http.RoundTripper) http.RoundTripper {
		return roundTripper(func(req *http.Request) (*http.Response, error) {
			if s.cut.Load() {
				return nil, ErrDisconnected
			}
			return next.RoundTrip(req)
		})
	})
	return cfg
}

type roundTripper func(*http.Request) (*http.Response, error)

func (f roundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}
