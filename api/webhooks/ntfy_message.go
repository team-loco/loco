package webhooks

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/webhooksig"
	"github.com/team-loco/loco/internal/clip"
)

// ntfy priorities.
const (
	ntfyDefault = 3
	ntfyHigh    = 4
	ntfyUrgent  = 5

	maxNtfyMessage = 300
	defaultTag     = "bell"
)

type notification struct {
	Title    string
	Message  string
	Priority int
	// Tags is a comma-separated list of ntfy tags.
	Tags string
	// Click is the dashboard page the notification opens; empty when the event has none.
	Click string
}

// notificationFor builds the notification for an event. webURL is the dashboard's base URL, empty when unset.
func notificationFor(
	row genDb.GetWebhookDeliveryPayloadRow,
	names genDb.GetEventNamesRow,
	webURL string,
) (notification, error) {
	// The json keys are the events.Field* names the recorders write.
	var data struct {
		Message       string  `json:"message"`
		Reason        string  `json:"reason"`
		Name          string  `json:"name"`
		Region        string  `json:"region"`
		Old           string  `json:"old"`
		New           string  `json:"new"`
		ResourceName  string  `json:"resource_name"`
		SilentSeconds float64 `json:"silentSeconds"`
	}
	if err := json.Unmarshal(row.Data, &data); err != nil {
		return notification{}, fmt.Errorf("decode event data: %w", err)
	}
	resource := names.ResourceName
	if resource == "" {
		resource = data.ResourceName
	}
	where := strings.Join(nonEmpty(names.OrgName, names.WorkspaceName, resource), " / ")
	cluster := clusterLabel(data.Name, data.Region)
	n := notification{Priority: ntfyDefault}
	switch row.Type {
	case events.BuildFailed:
		n.Title, n.Priority, n.Tags = "Build failed", ntfyHigh, "x"
		n.Message = fmt.Sprintf("Build of %s failed: %s", where, data.Message)
	case events.DeploymentFailed:
		n.Title, n.Priority, n.Tags = "Deployment failed", ntfyHigh, "x"
		n.Message = fmt.Sprintf("Deployment of %s failed: %s", where, data.Message)
	case events.DeploymentCrashed:
		n.Title, n.Priority, n.Tags = "Deployment crashed", ntfyHigh, "x"
		n.Message = fmt.Sprintf("Deployment of %s keeps restarting: %s", where, data.Message)
	case events.ClusterSilent:
		n.Title, n.Priority, n.Tags = "Cluster silent", ntfyUrgent, "rotating_light,cluster"
		n.Message = fmt.Sprintf("Cluster %s stopped reporting.", cluster)
	case events.ClusterRecovered:
		n.Title, n.Tags = "Cluster recovered", "white_check_mark,cluster"
		n.Message = fmt.Sprintf(
			"Cluster %s reports again after %s.",
			cluster,
			time.Duration(data.SilentSeconds*float64(time.Second)).Round(time.Second),
		)
	case events.ClusterHealthChanged:
		n.Title, n.Tags = "Cluster health changed", "warning,cluster"
		n.Message = fmt.Sprintf(
			"Cluster %s went from %s to %s.", cluster, data.Old, data.New,
		)
	case events.OrgSuspended:
		n.Title, n.Tags = "Organization suspended", "no_entry"
		n.Message = fmt.Sprintf("%s was suspended: %s", names.OrgName, data.Reason)
	default:
		n.Title, n.Tags = eventTitle(row.Type), defaultTag
		n.Message = n.Title
		if names := nonEmpty(where, clusterLabel(data.Name, data.Region)); len(names) > 0 {
			n.Message += ": " + strings.Join(names, " / ")
		}
	}
	n.Message = clip.Bytes(strings.Join(strings.Fields(n.Message), " "), maxNtfyMessage)
	n.Click = clickURL(row, webURL)
	return n, nil
}

func clusterLabel(name, region string) string {
	if name == "" {
		return ""
	}
	return fmt.Sprintf("%s (%s)", name, region)
}

// eventTitle turns "cluster.registered" into "Cluster registered".
func eventTitle(eventType string) string {
	words := strings.NewReplacer(".", " ", "_", " ").Replace(eventType)
	return strings.ToUpper(words[:1]) + words[1:]
}

func nonEmpty(values ...string) []string {
	var kept []string
	for _, value := range values {
		if value != "" {
			kept = append(kept, value)
		}
	}
	return kept
}

// clickURL points at the resource page, or the organization's audit page for a suspension; the dashboard has
// no cluster page.
func clickURL(row genDb.GetWebhookDeliveryPayloadRow, webURL string) string {
	base := strings.TrimRight(webURL, "/")
	switch {
	case base == "" || row.OrgID == nil:
		return ""
	case row.Type == events.OrgSuspended:
		return fmt.Sprintf("%s/org/%s/audit", base, row.OrgID)
	case row.WorkspaceID != nil && row.ResourceID != nil:
		return fmt.Sprintf("%s/org/%s/wks/%s/resource/%s", base, row.OrgID, row.WorkspaceID, row.ResourceID)
	}
	return ""
}

// sendNtfy publishes the event to the topic: the message is the body, the rest are query parameters.
func (d *Dispatcher) sendNtfy(
	ctx context.Context,
	row genDb.GetWebhookDeliveryPayloadRow,
	token string,
) error {
	names, err := d.queries.GetEventNames(ctx, genDb.GetEventNamesParams{
		OrgID: row.OrgID, WorkspaceID: row.WorkspaceID, ResourceID: row.ResourceID,
	})
	if err != nil {
		return fmt.Errorf("look up event names: %w", err)
	}
	n, err := notificationFor(row, names, d.cfg.WebURL)
	if err != nil {
		return err
	}
	query := url.Values{"title": {n.Title}, "priority": {strconv.Itoa(n.Priority)}}
	if n.Tags != "" {
		query.Set("tags", n.Tags)
	}
	if n.Click != "" {
		query.Set("click", n.Click)
	}
	target := row.Url + "?" + query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(n.Message))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")
	req.Header.Set("User-Agent", "Loco-Webhooks")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client, err := d.clientFor(row.Kind)
	if err != nil {
		return err
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if _, err := io.Copy(io.Discard, io.LimitReader(res.Body, webhooksig.MaxResponseBytes)); err != nil {
		return fmt.Errorf("drain ntfy response: %w", err)
	}
	if res.StatusCode < http.StatusOK || res.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("endpoint returned %s", res.Status)
	}
	return nil
}
