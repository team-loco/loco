package clickhouse

import (
	"strings"
	"testing"

	"github.com/team-loco/loco/internal/logquery"
)

func FuzzLogFilterSQLBindsEveryValue(f *testing.F) {
	for _, seed := range []string{
		`@path:"/a:b" OR -@level:err*`, "@status:200..299 (@resource:web* OR timeout)",
		`"'; DROP TABLE otel_logs; --"`, "@trace:ABC* -@region:us-* @replica:web-1 @build:b",
		`@user.name:"o'brien" @roles[0]:admin @ms:>=1e3`,
	} {
		f.Add(seed)
	}
	resources := []ResourceName{{ID: testResourceID, Name: "web"}}
	f.Fuzz(func(t *testing.T, query string) {
		expr, err := logquery.Parse(query)
		if err != nil || expr == nil {
			return
		}
		b := &positionalBinder{}
		sql := logFilterSQL(expr, resources, b)
		if placeholders := strings.Count(sql, "?"); placeholders != len(b.args) {
			t.Fatalf("%q compiled to %d placeholders for %d arguments: %s", query, placeholders, len(b.args), sql)
		}
		if strings.ContainsAny(sql, "'\"`\\;") {
			t.Fatalf("%q compiled to SQL with a literal: %s", query, sql)
		}
	})
}

func TestValidateLogFilterRejectsReservedKeysAndNumbersOnLogFields(t *testing.T) {
	const reservedWorkspace = "@loco.workspace.id is reserved; use @resource or @region"
	for query, want := range map[string]string{
		"@level:error @loco.io/build-id:b @status:>=500": "",
		"a (@loco.workspace.id:w)":                       reservedWorkspace + " at position 4",
		"-@level:>1":                                     "@level does not compare numbers at position 2",
		"@region:1..2":                                   "@region does not compare numbers at position 1",
	} {
		expr, err := logquery.Parse(query)
		if err != nil {
			t.Fatal(err)
		}
		err = ValidateLogFilter(expr)
		if (want == "" && err != nil) || (want != "" && (err == nil || err.Error() != want)) {
			t.Errorf("ValidateLogFilter(%q) = %v, want %q", query, err, want)
		}
	}
}
