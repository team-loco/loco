package clickhouse

import (
	"strings"

	"github.com/team-loco/loco/internal/logquery"
	"github.com/team-loco/loco/internal/systemtenant"
)

// systemFieldAttributes maps the names the system scope filters by to the resource attributes of
// Loco's own telemetry, rather than log, span or data-point attributes of the same name.
var systemFieldAttributes = map[string]string{
	"component": "service.name",
	"cluster":   "loco.cluster.id",
	"node":      "k8s.node.name",
}

// resourceAttributePrefix marks a field key that names a resource attribute only; no parsed key
// contains it.
const resourceAttributePrefix = "\x00resource:"

// SystemFields rewrites the identity fields of a system-scope query to the resource
// attributes of the sender, never a log or span attribute of the same name.
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
			expr.Key = resourceAttributePrefix + attribute
		}
		return expr
	}
	return expr
}

// resourceAttributeSQL matches a field SystemFields rewrote against the resource attribute it names.
func resourceAttributeSQL(field logquery.Field, b binder) (string, bool) {
	attribute, ok := strings.CutPrefix(field.Key, resourceAttributePrefix)
	if !ok {
		return "", false
	}
	return stringMatch("ResourceAttributes["+b.str(attribute)+"]", field, b), true
}

func systemTerms(terms []logquery.Expr) []logquery.Expr {
	rewritten := make([]logquery.Expr, len(terms))
	for i, term := range terms {
		rewritten[i] = SystemFields(term)
	}
	return rewritten
}

// metricAttribute is the column expression a metrics request's attribute key filters: the data
// point's own attribute, or a resource identity attribute in the system scope.
func metricAttribute(workspaceID, key string, b binder) string {
	if attribute, ok := systemFieldAttributes[key]; ok && workspaceID == systemtenant.WorkspaceID {
		return "ResourceAttributes[" + b.str(attribute) + "]"
	}
	return "Attributes[" + b.str(key) + "]"
}
