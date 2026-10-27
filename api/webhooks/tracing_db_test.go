package webhooks_test

import (
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/tracingtest"
	"github.com/team-loco/loco/api/webhooks"
)

const (
	deliverySpanName = "webhook.deliver"
	kindAttribute    = attribute.Key("loco.webhook.kind")
	traceparentKey   = "traceparent"
)

func (r *receiver) onlyTraceparent(t *testing.T) string {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.traceparents) != 1 {
		t.Fatalf("receiver got %d deliveries, want 1", len(r.traceparents))
	}
	return r.traceparents[0]
}

func deliverySpans(t *testing.T, spans []sdktrace.ReadOnlySpan) map[string]sdktrace.ReadOnlySpan {
	t.Helper()
	byKind := map[string]sdktrace.ReadOnlySpan{}
	for _, span := range spans {
		if span.Name() != deliverySpanName {
			continue
		}
		for _, attr := range span.Attributes() {
			if attr.Key == kindAttribute {
				byKind[attr.Value.AsString()] = span
			}
		}
	}
	return byKind
}

func TestDeliveriesLinkTheEventAndPropagateOnlyToInstallWebhooks(t *testing.T) {
	recorder := tracingtest.Record(t)
	f := newFixture(t)
	installSecret := newSecret(t)
	f.sync(t, webhooks.InstallWebhook{URL: installAll, Secret: installSecret, EventTypes: []string{}})
	install := newReceiver(t, installSecret)
	f.setURL(t, f.installRows(t)[installAll].id, install.srv.URL)
	hook, workspaceSecret := f.webhook(t, f.workspaceID)
	workspace := newReceiver(t, workspaceSecret)
	f.setURL(t, hook.ID, workspace.srv.URL)

	request := tracingtest.StartParent(t)
	workspaceID := f.workspaceID
	if err := events.Record(request, f.queries, events.Event{
		Type: events.ResourceUpdated, WorkspaceID: &workspaceID,
	}); err != nil {
		t.Fatalf("record: %v", err)
	}
	d := webhooks.NewDispatcher(f.queries, webhooks.NewClient(true), webhooks.NewClient(true))
	if sent, err := d.DispatchDue(t.Context()); err != nil || sent != 2 {
		t.Fatalf("dispatch = %d, %v", sent, err)
	}

	requestSpan := trace.SpanContextFromContext(request)
	spans := deliverySpans(t, recorder.Ended())
	for _, kind := range []genDb.WebhookKind{genDb.WebhookKindInstall, genDb.WebhookKindWorkspace} {
		span, ok := spans[string(kind)]
		if !ok {
			t.Fatalf("no %s delivery span", kind)
		}
		if span.Parent().IsValid() || span.SpanContext().TraceID() == requestSpan.TraceID() {
			t.Errorf("%s delivery span continues the request's trace instead of starting its own", kind)
		}
		links := span.Links()
		if len(links) != 1 || links[0].SpanContext.SpanID() != requestSpan.SpanID() {
			t.Errorf("%s delivery span links = %v, want the recording request span", kind, links)
		}
	}

	carrier := propagation.MapCarrier{traceparentKey: install.onlyTraceparent(t)}
	extracted := propagation.TraceContext{}.Extract(t.Context(), carrier)
	received := trace.SpanContextFromContext(extracted)
	installSpan := spans[string(genDb.WebhookKindInstall)].SpanContext()
	if received.TraceID() != installSpan.TraceID() || received.SpanID() != installSpan.SpanID() {
		t.Errorf("install webhook traceparent = %v, want the delivery span %v", received, installSpan)
	}
	if got := workspace.onlyTraceparent(t); got != "" {
		t.Errorf("workspace webhook received traceparent %q", got)
	}
}
