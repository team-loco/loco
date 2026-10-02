package service

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/team-loco/loco/api/pkg/commandbus"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
)

func TestCommandToProtoDeleteCarriesResourceID(t *testing.T) {
	payload, err := json.Marshal(DeleteCommandPayload{DeploymentID: "d1", ResourceID: "r1"})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	got, err := commandToProto(&commandbus.Command{
		ID:      uuid.New(),
		Type:    commandbus.CommandTypeDelete,
		Payload: payload,
	})
	if err != nil {
		t.Fatalf("commandToProto: %v", err)
	}

	if got.GetType() != agentv1.CommandType_COMMAND_TYPE_DELETE {
		t.Fatalf("type = %v, want delete", got.GetType())
	}
	if id := got.GetDelete().GetResourceId(); id != "r1" {
		t.Fatalf("resource id = %q, want %q", id, "r1")
	}
}

func TestCommandToProtoDeleteRejectsMalformedPayload(t *testing.T) {
	_, err := commandToProto(&commandbus.Command{
		ID:      uuid.New(),
		Type:    commandbus.CommandTypeDelete,
		Payload: []byte("not json"),
	})
	if err == nil {
		t.Fatal("commandToProto accepted a malformed delete payload")
	}
}
