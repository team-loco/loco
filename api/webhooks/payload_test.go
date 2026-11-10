package webhooks

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
)

func TestRuntimePayloadIncludesResourceAndEnvironment(t *testing.T) {
	resourceID, environmentID := uuid.New(), uuid.New()
	payload := payloadFrom(genDb.GetWebhookDeliveryPayloadRow{
		Type: events.ResourceDegraded, ResourceID: &resourceID, EnvironmentID: &environmentID,
		Data: []byte(`{"reason":"crash_looping","message":"The service keeps exiting."}`),
	})
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	fields := map[string]string{
		"resourceId": resourceID.String(), "environmentId": environmentID.String(), "type": events.ResourceDegraded,
	}
	for key, want := range fields {
		var got string
		if err := json.Unmarshal(decoded[key], &got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("%s = %q, want %q", key, got, want)
		}
	}
}
