// Package capacity exports effective Kubernetes pod requests for the platform collector.
package capacity

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/fields"
	podresource "k8s.io/component-helpers/resource"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/team-loco/loco/internal/capacitymetrics"
	locolabels "github.com/team-loco/loco/k8sapi/labels"
)

const pageSize = 500

var labels = []string{capacitymetrics.Source, capacitymetrics.Node, capacitymetrics.PodUID,
	capacitymetrics.Pod, capacitymetrics.Namespace, capacitymetrics.Organization, capacitymetrics.Workspace,
	capacitymetrics.Environment, capacitymetrics.Resource, capacitymetrics.Build}

// Handler serves a complete collection on the elected controller selected by the collector.
// Stream one API page at a time, outside the controller's SDK registry. Aborting a
// failed HTTP stream makes the Prometheus receiver roll back the entire scrape.
func Handler(reader client.Reader, elected <-chan struct{}) http.Handler {
	source := uuid.NewString()
	serial := make(chan struct{}, 1)
	var previous time.Time
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-elected:
		default:
			return
		}
		// Bound scrape memory and keep millisecond timestamps unique within this source.
		select {
		case serial <- struct{}{}:
			defer func() { <-serial }()
		case <-r.Context().Done():
			return
		}
		at := time.Now().UTC().Truncate(time.Millisecond)
		if !at.After(previous) {
			at = previous.Add(time.Millisecond)
		}
		previous = at
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		out := bufio.NewWriter(w)
		c := collection{ctx: r.Context(), reader: reader, source: source, at: at}
		if err := c.write(out); err != nil {
			slog.ErrorContext(r.Context(), "collect capacity", "error", err)
			panic(http.ErrAbortHandler)
		}
		if err := out.Flush(); err != nil {
			slog.ErrorContext(r.Context(), "write capacity", "error", err)
			panic(http.ErrAbortHandler)
		}
	})
}

type collection struct {
	ctx    context.Context
	reader client.Reader
	source string
	at     time.Time
}

func (c *collection) write(out io.Writer) error {
	for _, name := range []string{capacitymetrics.CPU, capacitymetrics.Memory, capacitymetrics.Pods} {
		if _, err := fmt.Fprintf(out, "# TYPE %s gauge\n", name); err != nil {
			return err
		}
	}
	total, err := c.pods(out)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(
		out,
		"%s{%s=%q} %d %d\n",
		capacitymetrics.Pods,
		capacitymetrics.Source,
		c.source,
		total,
		c.at.UnixMilli(),
	)
	return err
}

func (c *collection) pods(out io.Writer) (int, error) {
	total := 0
	continuation := ""
	selector := fields.AndSelectors(
		fields.OneTermNotEqualSelector("status.phase", string(corev1.PodSucceeded)),
		fields.OneTermNotEqualSelector("status.phase", string(corev1.PodFailed)),
	)
	for {
		var pods corev1.PodList
		err := c.reader.List(
			c.ctx,
			&pods,
			&client.ListOptions{Limit: pageSize, Continue: continuation, FieldSelector: selector},
		)
		if err != nil {
			return 0, fmt.Errorf("list capacity pods: %w", err)
		}
		for index := range pods.Items {
			pod := &pods.Items[index]
			if !assignedActive(pod) {
				continue
			}
			identity, err := c.identity(pod)
			if err != nil {
				return 0, err
			}
			// These status fields exist only when in-place resizing is enabled. Using
			// them preserves requests still allocated while a resize is pending.
			requests := podresource.PodRequests(
				pod,
				podresource.PodResourcesOptions{
					UseStatusResources: true,
					InPlacePodLevelResourcesVerticalScalingEnabled: true,
				},
			)
			attrs := make([]string, len(labels))
			for i, value := range identity {
				attrs[i] = labels[i] + "=" + strconv.Quote(value)
			}
			dimensions := strings.Join(attrs, ",")
			_, err = fmt.Fprintf(
				out,
				"%s{%s} %s %d\n%s{%s} %d %d\n",
				capacitymetrics.CPU,
				dimensions,
				strconv.FormatFloat(float64(requests.Cpu().MilliValue())/1000, 'g', -1, 64),
				c.at.UnixMilli(),
				capacitymetrics.Memory,
				dimensions,
				requests.Memory().Value(),
				c.at.UnixMilli(),
			)
			if err != nil {
				return 0, err
			}
			total++
		}
		continuation = pods.Continue
		if continuation == "" {
			return total, nil
		}
	}
}

func assignedActive(pod *corev1.Pod) bool {
	if pod.Spec.NodeName == "" {
		return false
	}
	switch pod.Status.Phase {
	case corev1.PodSucceeded, corev1.PodFailed:
		return false
	default:
		return true
	}
}

func (c *collection) identity(pod *corev1.Pod) ([]string, error) {
	attrs := pod.Labels
	org, workspace, environment, resource, build := "", "", "", "", ""
	if attrs[locolabels.LabelManagedBy] == locolabels.ControllerManagedBy {
		org, workspace = attrs[locolabels.LabelOrganizationID], attrs[locolabels.LabelWorkspaceID]
		resource, build = attrs[locolabels.LabelResourceID], attrs[locolabels.LabelBuildID]
		ids := []string{org, workspace, resource}
		if build != "" {
			ids = append(ids, build)
		} else {
			environment = attrs[locolabels.LabelEnvironmentID]
			ids = append(ids, environment)
		}
		for _, raw := range ids {
			id, err := uuid.Parse(raw)
			if err != nil || id == uuid.Nil {
				return nil, fmt.Errorf("managed pod %s/%s has an invalid capacity identity", pod.Namespace, pod.Name)
			}
		}
	}
	return []string{
		c.source,
		pod.Spec.NodeName,
		string(pod.UID),
		pod.Name,
		pod.Namespace,
		org,
		workspace,
		environment,
		resource,
		build,
	}, nil
}
