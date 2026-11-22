package db

import (
	"testing"
)

func TestQueryName(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"-- name: CountWorkspaceContents :many\nSELECT w.id FROM workspaces w": "CountWorkspaceContents",
		"-- name: NotifyClusterPlacements :exec\nSELECT pg_notify($1, '')":     "NotifyClusterPlacements",
		Named("ListAuditEvents", "WITH e AS (SELECT 1) SELECT * FROM e"):       "ListAuditEvents",
		"select 1": "SELECT",
		"begin":    "BEGIN",
	}
	for sql, want := range cases {
		if got := queryName(t.Context(), sql); got != want {
			t.Errorf("queryName(%q) = %q, want %q", sql, got, want)
		}
	}
}
