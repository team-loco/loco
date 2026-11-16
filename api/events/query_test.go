package events

import (
	"strconv"
	"strings"
	"testing"

	"github.com/team-loco/loco/internal/logquery"
)

// compiledLiterals are the only quoted strings querySQL writes itself.
var compiledLiterals = strings.NewReplacer(`'\'`, "", `'name'`, "", `'domain'`, "", `'names'`, "", `'array'`, "")

func FuzzQuerySQLBindsEveryValue(f *testing.F) {
	for _, seed := range []string{
		`@actor:"o'brien" OR -@target:shop*`, `"'; DROP TABLE events; --" @type:secret.*`,
		`100% a_b \ @request:x`, `-(@actor_type:system OR @target:"a\\b")`,
		"@actor:0198f2a4-6c1e-7c3b-9d4e-2f6a1b3c5d7e",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, query string) {
		expr, err := logquery.Parse(query)
		if err != nil || expr == nil || ValidateQuery(expr) != nil {
			return
		}
		b := &binder{}
		sql := querySQL(expr, b)
		for n := 1; n <= len(b.args); n++ {
			if !strings.Contains(sql, "$"+strconv.Itoa(n)) {
				t.Fatalf("%q compiled without $%d: %s", query, n, sql)
			}
		}
		if strings.Contains(sql, "$"+strconv.Itoa(len(b.args)+1)) {
			t.Fatalf("%q compiled to more placeholders than its %d arguments: %s", query, len(b.args), sql)
		}
		if rest := compiledLiterals.Replace(sql); strings.ContainsAny(rest, "'\";") || strings.Contains(rest, "--") {
			t.Fatalf("%q compiled to SQL with a literal: %s", query, sql)
		}
	})
}
