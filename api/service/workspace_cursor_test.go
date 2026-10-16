package service

import (
	"encoding/base64"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
)

func TestWorkspaceCursorRetainsTheExactSortPosition(t *testing.T) {
	id := uuid.New()
	at := time.Date(2026, 10, 16, 8, 20, 30, 123456000, time.FixedZone("offset", 3600))
	cursor, err := decodeWorkspaceCursor(encodeWorkspaceCursor(id, at))
	if err != nil {
		t.Fatal(err)
	}
	if cursor.ID == nil || *cursor.ID != id.String() || cursor.CreatedAt == nil || !cursor.CreatedAt.Equal(at) {
		t.Fatalf("cursor = %+v", cursor)
	}
	legacy, err := decodeWorkspaceCursor(encodeCursor(id.String()))
	if err != nil || legacy.ID == nil || *legacy.ID != id.String() || legacy.CreatedAt != nil {
		t.Fatalf("legacy cursor = %+v, error=%v", legacy, err)
	}
	empty, err := decodeWorkspaceCursor("")
	if err != nil || empty.ID != nil || empty.CreatedAt != nil {
		t.Fatalf("empty cursor = %+v, error=%v", empty, err)
	}
}

func TestWorkspaceCursorRejectsMalformedPositions(t *testing.T) {
	id := uuid.NewString()
	payloads := []string{
		"v2|2026-10-16T08:20:30Z|" + id,
		"v1|not-a-time|" + id,
		"v1|2026-10-16T08:20:30Z|not-a-uuid",
		"v1|0000-01-01T00:00:00Z|" + id,
		"v1|missing",
	}
	for _, payload := range payloads {
		token := base64.URLEncoding.EncodeToString([]byte(payload))
		if _, err := decodeWorkspaceCursor(token); err == nil {
			t.Fatalf("accepted %q", payload)
		}
	}
	if _, err := decodeWorkspaceCursor("%%%"); err == nil {
		t.Fatal("accepted non-base64 cursor")
	}
}

func TestLegacyWorkspaceCursorRequiresItsAnchor(t *testing.T) {
	f := newDeployFixture(t)
	server := NewWorkspaceServer(f.pool, f.queries, nil)
	cursor, err := server.workspacePageCursor(t.Context(), encodeCursor(f.workspaceID(t).String()))
	if err != nil || cursor.CreatedAt == nil {
		t.Fatalf("legacy position = %+v, error=%v", cursor, err)
	}
	_, err = server.workspacePageCursor(t.Context(), encodeCursor(uuid.NewString()))
	wantCode(t, err, connect.CodeInvalidArgument)
}
