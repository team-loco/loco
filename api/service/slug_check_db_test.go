package service

import (
	"testing"

	"github.com/google/uuid"
)

const (
	tableResources = "resources"
	columnPartial  = "partial"
	columnSlug     = "slug"
)

func TestDatabaseRejectsSlugsThatStartWithADigit(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	for _, tc := range []struct {
		column, table string
		id            uuid.UUID
	}{
		{columnSlug, tableResources, f.resourceID},
		{columnPartial, tableResources, f.resourceID},
		{columnSlug, "environments", f.envID},
	} {
		update := "UPDATE " + tc.table + " SET " + tc.column + " = $2 WHERE id = $1"
		if _, err := f.pool.Exec(t.Context(), update, tc.id, "7cart"); err == nil {
			t.Errorf("%s.%s accepted a digit-led value", tc.table, tc.column)
		}
		if _, err := f.pool.Exec(t.Context(), update, tc.id, "cart7"); err != nil {
			t.Errorf("%s.%s rejected a letter-led value: %v", tc.table, tc.column, err)
		}
	}
}
