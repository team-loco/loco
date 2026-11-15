package migrations_test

import (
	"testing"

	"github.com/team-loco/loco/api/db/dbtest"
)

func TestForeignKeysHaveLeadingIndexes(t *testing.T) {
	pool := dbtest.NewPool(t)
	rows, err := pool.Query(t.Context(), `
		SELECT c.conrelid::regclass::text, c.conname
		FROM pg_constraint c
		JOIN pg_class r ON r.oid = c.conrelid
		JOIN pg_namespace n ON n.oid = r.relnamespace
		JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = c.conkey[1]
		WHERE c.contype = 'f' AND n.nspname = 'public'
		AND NOT EXISTS (
			SELECT 1 FROM pg_index i
			WHERE i.indrelid = c.conrelid AND i.indisvalid AND i.indisready
			AND i.indnkeyatts >= cardinality(c.conkey)
			AND c.conkey <@ ARRAY(
				SELECT k FROM unnest(i.indkey) WITH ORDINALITY AS keys(k, position)
				WHERE position <= cardinality(c.conkey)
			)
			AND (i.indpred IS NULL OR (
				cardinality(c.conkey) = 1
				AND pg_get_expr(i.indpred, i.indrelid) = format('(%I IS NOT NULL)', a.attname)
			))
		)
		ORDER BY 1, 2
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var table, constraint string
		if err := rows.Scan(&table, &constraint); err != nil {
			t.Fatal(err)
		}
		t.Errorf("%s: foreign key %s lacks a leading index", table, constraint)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}
