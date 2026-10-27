package egress

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"

	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

const (
	labelOwningGatewayName      = "gateway.envoyproxy.io/owning-gateway-name"
	labelOwningGatewayNamespace = "gateway.envoyproxy.io/owning-gateway-namespace"
	clusterStatPrefix           = "cluster."
	routeBytesStatSuffix        = ".upstream_cx_rx_bytes_total"
	statSeparator               = ": "
)

var routeCluster = regexp.MustCompile(`^httproute/([^/]+)/resource-([^/]+)-route/rule/[0-9]+$`)

type Scraper interface {
	RouteBytes(ctx context.Context, pod *corev1.Pod) (map[string]int64, error)
}

type ApplicationLister interface {
	Applications(ctx context.Context) ([]locov1alpha1.Application, error)
}

type Config struct {
	Namespace     string
	Gateway       string
	ScrapeTimeout time.Duration
}

type Meter struct {
	pods    kubernetes.Interface
	cfg     Config
	apps    ApplicationLister
	scraper Scraper
}

func New(pods kubernetes.Interface, cfg Config, apps ApplicationLister, scraper Scraper) *Meter {
	return &Meter{pods: pods, cfg: cfg, apps: apps, scraper: scraper}
}

func (m *Meter) Counters(ctx context.Context) ([]*agentv1.EgressCounter, bool) {
	selector := labels.SelectorFromSet(labels.Set{
		labelOwningGatewayName:      m.cfg.Gateway,
		labelOwningGatewayNamespace: m.cfg.Namespace,
	})
	listed, err := m.pods.CoreV1().Pods(m.cfg.Namespace).List(ctx, metav1.ListOptions{LabelSelector: selector.String()})
	if err != nil {
		slog.WarnContext(ctx, "failed to list gateway proxies", "error", err)
		return nil, false
	}
	apps, err := m.apps.Applications(ctx)
	if err != nil {
		slog.WarnContext(ctx, "failed to list Applications for gateway egress", "error", err)
		return nil, false
	}
	owners := ownersByResource(apps)
	complete := true
	var counters []*agentv1.EgressCounter
	for i := range listed.Items {
		pod := &listed.Items[i]
		if pod.Status.Phase != corev1.PodRunning {
			continue
		}
		routes, scrapeErr := m.scrape(ctx, pod)
		if scrapeErr != nil {
			slog.WarnContext(ctx, "failed to read gateway egress", "pod", pod.Name, "error", scrapeErr)
			complete = false
			continue
		}
		counters = append(counters, podCounters(pod, routes, owners)...)
	}
	return counters, complete
}

func (m *Meter) scrape(ctx context.Context, pod *corev1.Pod) (map[string]int64, error) {
	ctx, cancel := context.WithTimeout(ctx, m.cfg.ScrapeTimeout)
	defer cancel()
	return m.scraper.RouteBytes(ctx, pod)
}

func ownersByResource(apps []locov1alpha1.Application) map[string]*locov1alpha1.ApplicationSpec {
	owners := make(map[string]*locov1alpha1.ApplicationSpec, len(apps))
	for i := range apps {
		spec := &apps[i].Spec
		owners[spec.ResourceID] = spec
	}
	return owners
}

func podCounters(
	pod *corev1.Pod,
	routes map[string]int64,
	owners map[string]*locov1alpha1.ApplicationSpec,
) []*agentv1.EgressCounter {
	counters := make([]*agentv1.EgressCounter, 0, len(routes))
	for route, bytes := range routes {
		match := routeCluster.FindStringSubmatch(route)
		if match == nil {
			continue
		}
		namespace, resourceID := match[1], match[2]
		owner, ok := owners[resourceID]
		if !ok || !attributable(owner) || !strings.HasSuffix(namespace, owner.WorkspaceID) {
			continue
		}
		counters = append(counters, &agentv1.EgressCounter{
			GatewayPodUid:  string(pod.UID),
			Route:          route,
			OrganizationId: owner.OrganizationID,
			WorkspaceId:    owner.WorkspaceID,
			ResourceId:     owner.ResourceID,
			EnvironmentId:  owner.EnvironmentID,
			Bytes:          bytes,
		})
	}
	return counters
}

func attributable(owner *locov1alpha1.ApplicationSpec) bool {
	return owner.OrganizationID != "" && owner.WorkspaceID != "" && owner.EnvironmentID != ""
}

func ParseRouteBytes(stats io.Reader) (map[string]int64, error) {
	routes := make(map[string]int64)
	scanner := bufio.NewScanner(stats)
	for scanner.Scan() {
		name, value, ok := strings.Cut(scanner.Text(), statSeparator)
		if !ok {
			continue
		}
		route, ok := strings.CutPrefix(name, clusterStatPrefix)
		if !ok {
			continue
		}
		route, ok = strings.CutSuffix(route, routeBytesStatSuffix)
		if !ok {
			continue
		}
		bytes, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", name, err)
		}
		routes[route] = bytes
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read gateway stats: %w", err)
	}
	return routes, nil
}
