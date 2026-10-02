package service

import (
	"encoding/base64"
	"testing"

	"github.com/google/uuid"
)

func TestDecodeCursorRoundTrip(t *testing.T) {
	id := uuid.Must(uuid.NewV7())
	idString := id.String()
	token := encodeCursor(idString)

	got, err := decodeCursor(token)
	if err != nil {
		t.Fatalf("decodeCursor: %v", err)
	}
	if got != idString {
		t.Fatalf("decodeCursor = %q, want %q", got, idString)
	}
}

func TestDecodeCursorRejectsNonUUID(t *testing.T) {
	token := base64.URLEncoding.EncodeToString([]byte("not-a-uuid"))
	if _, err := decodeCursor(token); err == nil {
		t.Fatal("decodeCursor accepted a cursor that is not a uuid")
	}
	if _, err := decodeCursor("%%%"); err == nil {
		t.Fatal("decodeCursor accepted a cursor that is not base64")
	}
}
