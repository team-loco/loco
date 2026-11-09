package clickhouse

import (
	"encoding/json"
	"fmt"

	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
)

const (
	telemetryScopeAttribute = "loco.telemetry.scope"
	// kubernetesEventScope marks the Kubernetes events the worker's cluster collector attributes
	// to a resource; keep in sync with transform/kubernetes_events in charts/loco-worker/values.yaml.
	kubernetesEventScope = "kubernetes-event"
)

// watchEvent is the body the k8sobjects receiver stores for a watched events.k8s.io/v1 Event.
type watchEvent struct {
	Object struct {
		Reason    string `json:"reason"`
		Note      string `json:"note"`
		Type      string `json:"type"`
		Regarding struct {
			Kind string `json:"kind"`
			Name string `json:"name"`
		} `json:"regarding"`
	} `json:"object"`
}

// KubernetesEvent decodes a log entry of a QueryLogs or tail query with KubernetesEvents set.
func KubernetesEvent(entry *observabilityv1.LogEntry) (*observabilityv1.Event, error) {
	var body watchEvent
	if err := json.Unmarshal([]byte(entry.GetBody()), &body); err != nil {
		return nil, fmt.Errorf("decode kubernetes event: %w", err)
	}
	return &observabilityv1.Event{
		Timestamp:  entry.GetTimestamp(),
		Reason:     body.Object.Reason,
		Message:    body.Object.Note,
		Type:       body.Object.Type,
		ObjectKind: body.Object.Regarding.Kind,
		ObjectName: body.Object.Regarding.Name,
	}, nil
}
