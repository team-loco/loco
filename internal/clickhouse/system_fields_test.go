package clickhouse

import (
	"slices"
	"strings"
	"testing"

	"github.com/team-loco/loco/internal/logquery"
)

func TestSystemFieldsMatchOnlyTheSendersResourceAttributes(t *testing.T) {
	expr, err := logquery.Parse("@cluster:c1 -@component:loco-api @node:node-a")
	if err != nil {
		t.Fatal(err)
	}
	system := SystemFields(expr)
	for name, compile := range map[string]func(*positionalBinder) string{
		"logs": func(b *positionalBinder) string { return logFilterSQL(system, nil, b) },
		"traces": func(b *positionalBinder) string {
			return traceFilterSQL(system, nil, func() string { return "1" }, b)
		},
	} {
		b := &positionalBinder{}
		sql := compile(b)
		if strings.Contains(sql, "LogAttributes") || strings.Contains(sql, "SpanAttributes") {
			t.Errorf("%s: %s reads a record's own attributes", name, sql)
		}
		if strings.Count(sql, "ResourceAttributes[?]") != 3 {
			t.Errorf("%s: %s, want all identity fields on ResourceAttributes", name, sql)
		}
		want := []any{"loco.cluster.id", "c1", "service.name", "loco-api", "k8s.node.name", "node-a"}
		if !slices.Equal(b.args, want) {
			t.Errorf("%s: arguments %v, want %v", name, b.args, want)
		}
	}
}
