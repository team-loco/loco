package clickhouse

import (
	"github.com/team-loco/loco/internal/logquery"
	"github.com/team-loco/loco/internal/systemtenant"
)

// systemFieldAttributes maps the names the system scope filters by to the resource attributes of
// Loco's own telemetry: the component that sent it and the cluster it came from.
var systemFieldAttributes = map[string]string{
	"component": "service.name",
	"cluster":   "loco.cluster.id",
}

// SystemFields rewrites the component and cluster fields of a system-scope query to the attributes
// they stand for.
func SystemFields(expr logquery.Expr) logquery.Expr {
	switch expr := expr.(type) {
	case logquery.And:
		return logquery.And{Terms: systemTerms(expr.Terms)}
	case logquery.Or:
		return logquery.Or{Terms: systemTerms(expr.Terms)}
	case logquery.Not:
		return logquery.Not{Term: SystemFields(expr.Term)}
	case logquery.Field:
		if attribute, ok := systemFieldAttributes[expr.Key]; ok {
			expr.Key = attribute
		}
		return expr
	}
	return expr
}

func systemTerms(terms []logquery.Expr) []logquery.Expr {
	rewritten := make([]logquery.Expr, len(terms))
	for i, term := range terms {
		rewritten[i] = SystemFields(term)
	}
	return rewritten
}

// metricAttribute is the column expression a metrics request's attribute key filters: the data
// point's own attribute, or the sender's resource attribute for a system-scope component or cluster.
func metricAttribute(workspaceID, key string, b binder) string {
	if attribute, ok := systemFieldAttributes[key]; ok && workspaceID == systemtenant.WorkspaceID {
		return "ResourceAttributes[" + b.str(attribute) + "]"
	}
	return "Attributes[" + b.str(key) + "]"
}
