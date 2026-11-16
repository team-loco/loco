package service

import (
	"testing"

	"github.com/google/uuid"

	eventv1 "github.com/team-loco/loco/gen/go/loco/event/v1"
)

func TestListEventsRejectsQueriesWithThePosition(t *testing.T) {
	t.Parallel()
	server := NewEventServer(nil, nil)
	for query, want := range map[string]string{
		"(ada":                "unclosed ( at 1",
		"ada @status:200":     "unknown field @status; use @actor, @actor_type, @target, @type or @request at 5",
		"-@type:>1":           "@type does not compare numbers at 2",
		"@actor:1..2":         "@actor does not compare numbers at 1",
		"x (@request:<=5)":    "@request does not compare numbers at 4",
		`@target:"a`:          "unclosed quote at 9",
		"@target:a*b":         "* only ends a prefix, as in /api/* at 10",
		"@actor_type:system:": "quote values that contain : at 13",
	} {
		_, err := server.ListEvents(t.Context(), &eventv1.ListEventsRequest{OrgId: uuid.NewString(), Query: query})
		if got := querySyntaxDetails(t, err); got != want {
			t.Errorf("ListEvents(%q) error = %q, want %q", query, got, want)
		}
	}
}
