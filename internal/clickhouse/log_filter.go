package clickhouse

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/team-loco/loco/internal/logquery"
)

// ResourceName is a resource a log query can name with @resource.
type ResourceName struct {
	ID   string
	Name string
}

// levelSeverities lists the severity texts each @level name matches; info includes unset severities.
var levelSeverities = map[string][]string{
	"error": {"ERROR", "error", "Error", "FATAL", "fatal", "Fatal", "CRITICAL", "critical", "Critical"},
	"warn":  {"WARN", "warn", "Warn", "WARNING", "warning", "Warning"},
	"info":  {"", "INFO", "info", "Info", "Information", "NOTICE", "notice"},
	"debug": {"DEBUG", "debug", "Debug", "TRACE", "trace", "Trace"},
}

// Fields a log query names directly; every other key is a log attribute, falling back to a
// resource attribute. The web's query bar keeps a copy in web/src/pages/observability/query.ts.
const (
	LevelField    = "level"
	TraceField    = "trace"
	ResourceField = "resource"
	ReplicaField  = "replica"
	BuildField    = "build"
	RegionField   = "region"
)

var logFields = map[string]bool{
	LevelField: true, TraceField: true, ResourceField: true, ReplicaField: true, BuildField: true, RegionField: true,
}

const (
	podAttribute   = "k8s.pod.name"
	buildAttribute = "loco.io/build-id"

	reservedAttributePrefix = "loco."
	// keep in sync with the loco.io/ keys in k8sapi/labels and k8sapi/v1alpha1
	kubernetesLabelPrefix = "loco.io/"
)

// ValidateLogFilter rejects fields a log query cannot use: the loco. attributes Loco sets, apart
// from loco.io/ labels, and number comparisons on the log fields.
func ValidateLogFilter(expr logquery.Expr) error {
	switch expr := expr.(type) {
	case logquery.And:
		return validateLogFilters(expr.Terms)
	case logquery.Or:
		return validateLogFilters(expr.Terms)
	case logquery.Not:
		return ValidateLogFilter(expr.Term)
	case logquery.Field:
		if strings.HasPrefix(expr.Key, reservedAttributePrefix) && !strings.HasPrefix(expr.Key, kubernetesLabelPrefix) {
			return &logquery.SyntaxError{
				Position: expr.Position,
				Message:  fmt.Sprintf("@%s is reserved; use @resource or @region", expr.Key),
			}
		}
		if logFields[expr.Key] && expr.Match != logquery.Equal && expr.Match != logquery.Prefix {
			return &logquery.SyntaxError{
				Position: expr.Position,
				Message:  fmt.Sprintf("@%s does not compare numbers", expr.Key),
			}
		}
	}
	return nil
}

func validateLogFilters(terms []logquery.Expr) error {
	for _, term := range terms {
		if err := ValidateLogFilter(term); err != nil {
			return err
		}
	}
	return nil
}

// logFilterSQL compiles a parsed log query into a WHERE condition, binding every value through b.
// A field a log lacks reads as an empty string or as no number, so a negated field keeps that log.
func logFilterSQL(expr logquery.Expr, resources []ResourceName, b logBinder) string {
	switch expr := expr.(type) {
	case logquery.And:
		return joinFilters(expr.Terms, " AND ", resources, b)
	case logquery.Or:
		return joinFilters(expr.Terms, " OR ", resources, b)
	case logquery.Not:
		return "NOT " + logFilterSQL(expr.Term, resources, b)
	case logquery.Text:
		return "position(Body, " + b.str(expr.Value) + ") > 0"
	case logquery.Field:
		return fieldSQL(expr, resources, b)
	}
	panic("unknown log query expression")
}

func joinFilters(terms []logquery.Expr, operator string, resources []ResourceName, b logBinder) string {
	parts := make([]string, len(terms))
	for i, term := range terms {
		parts[i] = logFilterSQL(term, resources, b)
	}
	return "(" + strings.Join(parts, operator) + ")"
}

func fieldSQL(field logquery.Field, resources []ResourceName, b logBinder) string {
	switch field.Key {
	case LevelField:
		return levelSQL(field, b)
	case ResourceField:
		return resourceSQL(field, resources, b)
	case TraceField:
		field.Value = strings.ToLower(field.Value)
		return stringMatch("TraceId", field, b)
	case RegionField:
		field.Value = strings.ToLower(field.Value)
		return stringMatch("lowerUTF8(ResourceAttributes["+b.str(regionAttribute)+"])", field, b)
	case ReplicaField:
		return stringMatch("ResourceAttributes["+b.str(podAttribute)+"]", field, b)
	case BuildField:
		return stringMatch("ResourceAttributes["+b.str(buildAttribute)+"]", field, b)
	}
	switch field.Match {
	case logquery.Equal:
		return attributeEquals(field, b)
	case logquery.Prefix:
		return "(startsWith(LogAttributes[" + b.str(field.Key) + "], " + b.str(field.Value) + ") OR (NOT mapContains(" +
			"LogAttributes, " + b.str(field.Key) + ") AND startsWith(ResourceAttributes[" + b.str(field.Key) + "], " +
			b.str(field.Value) + ")))"
	}
	attribute := "if(mapContains(LogAttributes, " + b.str(field.Key) + "), LogAttributes[" + b.str(field.Key) +
		"], ResourceAttributes[" + b.str(field.Key) + "])"
	if field.Match == logquery.Between {
		return "ifNull(toFloat64OrNull(" + attribute + ") BETWEEN " + b.float(field.Low) + " AND " +
			b.float(field.High) + ", 0)"
	}
	operator := map[logquery.Match]string{
		logquery.Greater: ">", logquery.GreaterOrEqual: ">=", logquery.Less: "<", logquery.LessOrEqual: "<=",
	}[field.Match]
	return "ifNull(toFloat64OrNull(" + attribute + ") " + operator + " " + b.float(field.Low) + ", 0)"
}

// attributeEquals matches the log attribute, or the resource attribute when the log has no such key.
// The has() checks on the key=value item arrays let the text indexes skip granules; the map
// comparisons keep the match exact when a key or value contains =.
func attributeEquals(field logquery.Field, b logBinder) string {
	item := field.Key + "=" + field.Value
	return "((has(LogAttributeItems, " + b.str(item) + ") AND LogAttributes[" + b.str(field.Key) + "] = " +
		b.str(field.Value) + ") OR (NOT mapContains(LogAttributes, " + b.str(field.Key) + ") AND has(" +
		"ResourceAttributeItems, " + b.str(item) + ") AND ResourceAttributes[" + b.str(field.Key) + "] = " +
		b.str(field.Value) + "))"
}

func stringMatch(column string, field logquery.Field, b logBinder) string {
	if field.Match == logquery.Prefix {
		return "startsWith(" + column + ", " + b.str(field.Value) + ")"
	}
	return column + " = " + b.str(field.Value)
}

// levelSQL matches the severities of the level names the value selects, or the severity text itself
// when it names no level.
func levelSQL(field logquery.Field, b logBinder) string {
	value := strings.ToLower(field.Value)
	var severities []string
	for _, name := range slices.Sorted(maps.Keys(levelSeverities)) {
		if name == value || (field.Match == logquery.Prefix && strings.HasPrefix(name, value)) {
			severities = append(severities, levelSeverities[name]...)
		}
	}
	if len(severities) == 0 {
		return stringMatch("SeverityText", field, b)
	}
	return logIn("SeverityText", severities, b)
}

// resourceSQL matches the resources whose name equals the value or starts with it, ignoring case.
func resourceSQL(field logquery.Field, resources []ResourceName, b logBinder) string {
	value := strings.ToLower(field.Value)
	var ids []string
	for _, resource := range resources {
		name := strings.ToLower(resource.Name)
		if name == value || (field.Match == logquery.Prefix && strings.HasPrefix(name, value)) {
			ids = append(ids, resource.ID)
		}
	}
	if len(ids) == 0 {
		return "0"
	}
	return logIn(resourceColumn, ids, b)
}
