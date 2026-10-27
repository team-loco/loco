package egress

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
)

const (
	adminPort       = 19000
	loopbackAddress = "127.0.0.1"
	statsPath       = "/stats"
	statsFilter     = `^cluster\.httproute/.+\.upstream_cx_rx_bytes_total$`
	maxStatsBytes   = 16 << 20
)

var (
	errNoForwardedPort = errors.New("port-forward opened no local port")
	errStatsStatus     = errors.New("gateway admin answered")
)

type AdminScraper struct {
	config *rest.Config
	client kubernetes.Interface
}

func NewAdminScraper(config *rest.Config, client kubernetes.Interface) *AdminScraper {
	return &AdminScraper{config: config, client: client}
}

func (s *AdminScraper) RouteBytes(ctx context.Context, pod *corev1.Pod) (map[string]int64, error) {
	transport, upgrader, err := spdy.RoundTripperFor(s.config)
	if err != nil {
		return nil, fmt.Errorf("port-forward transport: %w", err)
	}
	target := s.client.CoreV1().RESTClient().Post().
		Resource("pods").
		Namespace(pod.Namespace).
		Name(pod.Name).
		SubResource("portforward").
		URL()
	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, http.MethodPost, target)
	stop := make(chan struct{})
	ready := make(chan struct{})
	ports := []string{"0:" + strconv.Itoa(adminPort)}
	forwarder, err := portforward.NewOnAddresses(
		dialer, []string{loopbackAddress}, ports, stop, ready, io.Discard, io.Discard,
	)
	if err != nil {
		return nil, fmt.Errorf("port-forward %s: %w", pod.Name, err)
	}
	forwardErr := make(chan error, 1)
	go func() { forwardErr <- forwarder.ForwardPorts() }()
	defer close(stop)
	select {
	case <-ready:
	case failed := <-forwardErr:
		return nil, fmt.Errorf("port-forward %s: %w", pod.Name, failed)
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	forwarded, err := forwarder.GetPorts()
	if err != nil {
		return nil, fmt.Errorf("port-forward %s: %w", pod.Name, err)
	}
	if len(forwarded) == 0 {
		return nil, errNoForwardedPort
	}
	return fetchRouteBytes(ctx, int(forwarded[0].Local))
}

func fetchRouteBytes(ctx context.Context, port int) (map[string]int64, error) {
	query := url.Values{"filter": {statsFilter}}
	endpoint := url.URL{
		Scheme:   "http",
		Host:     loopbackAddress + ":" + strconv.Itoa(port),
		Path:     statsPath,
		RawQuery: query.Encode(),
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build stats request: %w", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("read gateway stats: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("read gateway stats: %w %s", errStatsStatus, resp.Status)
	}
	body := io.LimitReader(resp.Body, maxStatsBytes)
	return ParseRouteBytes(body)
}
