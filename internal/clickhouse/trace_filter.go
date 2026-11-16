package clickhouse

import (
	"strings"

	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	"github.com/team-loco/loco/internal/logquery"
)

// Fields a trace query names directly besides TraceField and ResourceField; every other key is a
// span attribute, falling back to a resource attribute. The web's query bar keeps a copy in
// web/src/pages/observability/query.ts.
const (
	StatusField   = "status"
	DurationField = "duration"
)

// traceStatuses maps the @status values, ok and error, to their trace statuses.
var traceStatuses = func() map[string]observabilityv1.TraceStatus {
	statuses := make(map[string]observabilityv1.TraceStatus)
	for _, status := range []observabilityv1.TraceStatus{
		observabilityv1.TraceStatus_TRACE_STATUS_OK, observabilityv1.TraceStatus_TRACE_STATUS_ERROR,
	} {
		statuses[strings.ToLower(strings.TrimPrefix(status.String(), "TRACE_STATUS_"))] = status
	}
	return statuses
}()

var spanAttributes = attributeMap{column: "SpanAttributes", items: "SpanAttributeItems"}

// TraceFilter is a parsed trace query; Resources are the names its @resource fields match.
type TraceFilter struct {
	Query     logquery.Expr
	Resources []ResourceName
}

// ValidateTraceFilter rejects fields a trace query cannot use: the loco. attributes Loco sets, apart
// from loco.io/ labels, a @status other than ok or error, a @duration without a number comparison,
// and number comparisons on @trace and @resource.
func ValidateTraceFilter(expr logquery.Expr) error {
	return logquery.CheckFields(expr, func(field logquery.Field) error {
		if err := rejectReserved(field, "use @resource"); err != nil {
			return err
		}
		switch field.Key {
		case StatusField:
			if _, ok := traceStatuses[strings.ToLower(field.Value)]; !ok || field.Match != logquery.Equal {
				return fieldError(field, "@status is ok or error")
			}
		case DurationField:
			if field.Match == logquery.Equal || field.Match == logquery.Prefix {
				return fieldError(field, "@duration compares milliseconds, as in >100 or 100..500")
			}
		case TraceField, ResourceField:
			return rejectNumbers(field)
		}
		return nil
	})
}

// traceFilterSQL compiles a parsed trace query into a HAVING condition on the traces
// buildTraceSummaryQuery groups, binding every value through b. A @resource or attribute field
// matches a trace when any of its spans that spans selects matches it, through a subquery the
// table's indexes can skip granules for.
func traceFilterSQL(expr logquery.Expr, resources []ResourceName, spans func() string, b binder) string {
	return filterSQL(expr, func(text logquery.Text) string {
		return "positionCaseInsensitiveUTF8(root.1, " + b.str(text.Value) + ") > 0"
	}, func(field logquery.Field) string {
		return traceFieldSQL(field, resources, spans, b)
	})
}

func traceFieldSQL(field logquery.Field, resources []ResourceName, spans func() string, b binder) string {
	switch field.Key {
	case StatusField:
		return "status = " + b.num(int64(traceStatuses[strings.ToLower(field.Value)]))
	case DurationField:
		return numberSQL("duration_nanos / 1e6", field, b)
	case TraceField:
		field.Value = strings.ToLower(field.Value)
		return stringMatch("TraceId", field, b)
	}
	// spans binds its values before the match does, in the order they appear.
	where := spans()
	var match string
	if field.Key == ResourceField {
		match = resourceSQL(field, resources, b)
	} else {
		match = attributeSQL(field, spanAttributes, b)
	}
	return "TraceId IN (SELECT TraceId FROM otel_traces WHERE " + where + " AND " + match + ")"
}
