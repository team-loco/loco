package clickhouse

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	"github.com/team-loco/loco/internal/logquery"
)

// ResourceName is a resource a log query can name with @resource.
type ResourceName struct {
	ID   string
	Name string
}

// logLevels maps each level to the OpenTelemetry severity numbers it covers and to the lowercase
// severity texts that name it; a record without a severity number reads its level from the text.
var logLevels = []struct {
	level    observabilityv1.LogLevel
	low, top uint8
	texts    []string
}{
	{observabilityv1.LogLevel_LOG_LEVEL_DEBUG, 1, 8, []string{"debug", "trace"}},
	{observabilityv1.LogLevel_LOG_LEVEL_INFO, 9, 12, []string{"info", "information", "notice"}},
	{observabilityv1.LogLevel_LOG_LEVEL_WARN, 13, 16, []string{"warn", "warning"}},
	{observabilityv1.LogLevel_LOG_LEVEL_ERROR, 17, 24, []string{
		"error", "err", "fatal", "critical", "panic", "emergency", "alert",
	}},
}

// levelColumn is the LogLevel number of a log row; unset or unrecognized severities are info.
var levelColumn = func() string {
	numbers := make([]string, 0, len(logLevels))
	texts := make([]string, 0, len(logLevels))
	for _, l := range logLevels {
		level := strconv.Itoa(int(l.level))
		numbers = append(numbers, fmt.Sprintf("SeverityNumber BETWEEN %d AND %d, %s", l.low, l.top, level))
		texts = append(texts, "lowerUTF8(SeverityText) IN ('"+strings.Join(l.texts, "', '")+"'), "+level)
	}
	return "multiIf(" + strings.Join(numbers, ", ") + ", " + strings.Join(texts, ", ") + ", " +
		strconv.Itoa(int(observabilityv1.LogLevel_LOG_LEVEL_INFO)) + ")"
}()

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
// from loco.io/ labels, number comparisons on the log fields and @level values that name no level.
func ValidateLogFilter(expr logquery.Expr) error {
	return logquery.CheckFields(expr, func(field logquery.Field) error {
		if err := rejectReserved(field, "use @resource or @region"); err != nil {
			return err
		}
		if !logFields[field.Key] {
			return nil
		}
		if err := rejectNumbers(field); err != nil {
			return err
		}
		if field.Key == LevelField && len(levelsMatching(field)) == 0 {
			return fieldError(field, "@level is error, warn, info or debug")
		}
		return nil
	})
}

func rejectReserved(field logquery.Field, hint string) error {
	if strings.HasPrefix(field.Key, reservedAttributePrefix) && !strings.HasPrefix(field.Key, kubernetesLabelPrefix) {
		return fieldError(field, "@%s is reserved; %s", field.Key, hint)
	}
	return nil
}

func rejectNumbers(field logquery.Field) error {
	if field.Match != logquery.Equal && field.Match != logquery.Prefix {
		return fieldError(field, "@%s does not compare numbers", field.Key)
	}
	return nil
}

func fieldError(field logquery.Field, format string, args ...any) error {
	return &logquery.SyntaxError{Position: field.Position, Message: fmt.Sprintf(format, args...)}
}

// logFilterSQL compiles a parsed log query into a WHERE condition, binding every value through b.
// A field a log lacks reads as an empty string or as no number, so a negated field keeps that log.
func logFilterSQL(expr logquery.Expr, resources []ResourceName, b binder) string {
	return filterSQL(expr, func(text logquery.Text) string {
		return "position(Body, " + b.str(text.Value) + ") > 0"
	}, func(field logquery.Field) string {
		return fieldSQL(field, resources, b)
	})
}

// filterSQL compiles the AND, OR and negation of a parsed query, with text and field compiling its
// terms in query order.
func filterSQL(expr logquery.Expr, text func(logquery.Text) string, field func(logquery.Field) string) string {
	switch expr := expr.(type) {
	case logquery.And:
		return joinFilters(expr.Terms, " AND ", text, field)
	case logquery.Or:
		return joinFilters(expr.Terms, " OR ", text, field)
	case logquery.Not:
		return "NOT " + filterSQL(expr.Term, text, field)
	case logquery.Text:
		return text(expr)
	case logquery.Field:
		return field(expr)
	}
	panic("unknown query expression")
}

func joinFilters(
	terms []logquery.Expr,
	operator string,
	text func(logquery.Text) string,
	field func(logquery.Field) string,
) string {
	parts := make([]string, len(terms))
	for i, term := range terms {
		parts[i] = filterSQL(term, text, field)
	}
	return "(" + strings.Join(parts, operator) + ")"
}

func fieldSQL(field logquery.Field, resources []ResourceName, b binder) string {
	switch field.Key {
	case LevelField:
		return levelSQL(field)
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
	return attributeSQL(field, logAttributes, b)
}

// attributeMap is a table's own attribute map, read before ResourceAttributes, and its key=value
// item array.
type attributeMap struct{ column, items string }

var logAttributes = attributeMap{column: "LogAttributes", items: "LogAttributeItems"}

// attributeSQL matches the record's own attribute, or the resource attribute when the record has no
// such key.
func attributeSQL(field logquery.Field, own attributeMap, b binder) string {
	switch field.Match {
	case logquery.Equal:
		return attributeEquals(field, own, b)
	case logquery.Prefix:
		return "(startsWith(" + own.column + "[" + b.str(field.Key) + "], " + b.str(field.Value) + ") OR (NOT " +
			"mapContains(" + own.column + ", " + b.str(field.Key) + ") AND startsWith(ResourceAttributes[" +
			b.str(field.Key) + "], " + b.str(field.Value) + ")))"
	}
	attribute := "if(mapContains(" + own.column + ", " + b.str(field.Key) + "), " + own.column + "[" +
		b.str(field.Key) + "], ResourceAttributes[" + b.str(field.Key) + "])"
	return "ifNull(" + numberSQL("toFloat64OrNull("+attribute+")", field, b) + ", 0)"
}

// attributeEquals is attributeSQL for an exact value. The has() checks on the key=value item arrays
// let the text indexes skip granules; the map comparisons keep the match exact when a key or value
// contains =.
func attributeEquals(field logquery.Field, own attributeMap, b binder) string {
	item := field.Key + "=" + field.Value
	return "((has(" + own.items + ", " + b.str(item) + ") AND " + own.column + "[" + b.str(field.Key) + "] = " +
		b.str(field.Value) + ") OR (NOT mapContains(" + own.column + ", " + b.str(field.Key) + ") AND has(" +
		"ResourceAttributeItems, " + b.str(item) + ") AND ResourceAttributes[" + b.str(field.Key) + "] = " +
		b.str(field.Value) + "))"
}

// numberSQL compares the Float64 expression value with the field's number or range.
func numberSQL(value string, field logquery.Field, b binder) string {
	if field.Match == logquery.Between {
		return value + " BETWEEN " + b.float(field.Low) + " AND " + b.float(field.High)
	}
	operator := map[logquery.Match]string{
		logquery.Greater: ">", logquery.GreaterOrEqual: ">=", logquery.Less: "<", logquery.LessOrEqual: "<=",
	}[field.Match]
	return value + " " + operator + " " + b.float(field.Low)
}

func stringMatch(column string, field logquery.Field, b binder) string {
	if field.Match == logquery.Prefix {
		return "startsWith(" + column + ", " + b.str(field.Value) + ")"
	}
	return column + " = " + b.str(field.Value)
}

// levelsMatching returns the levels a @level value names: a level whose severity texts include the
// value, ignoring case, or start with it for a prefix match.
func levelsMatching(field logquery.Field) []string {
	value := strings.ToLower(field.Value)
	var levels []string
	for _, l := range logLevels {
		if slices.ContainsFunc(l.texts, func(text string) bool {
			return text == value || (field.Match == logquery.Prefix && strings.HasPrefix(text, value))
		}) {
			levels = append(levels, strconv.Itoa(int(l.level)))
		}
	}
	return levels
}

func levelSQL(field logquery.Field) string {
	return levelColumn + " IN (" + strings.Join(levelsMatching(field), ",") + ")"
}

// resourceSQL matches the resources whose name equals the value or starts with it, ignoring case.
func resourceSQL(field logquery.Field, resources []ResourceName, b binder) string {
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
	return resourceIn(ids, b)
}
