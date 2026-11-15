package events

import (
	"errors"
	"testing"
)

func TestRecordRequiresActor(t *testing.T) {
	if err := Record(t.Context(), nil, Event{Type: UserCreated}); !errors.Is(err, errActorRequired) {
		t.Fatalf("record without actor: %v", err)
	}
}
