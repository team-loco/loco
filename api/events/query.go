package events

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/team-loco/loco/internal/logquery"
)

// Fields an audit query names.
const (
	actorField     = "actor"
	actorTypeField = "actor_type"
	targetField    = "target"
	typeField      = "type"
	requestField   = "request"
)

var queryFields = map[string]bool{
	actorField: true, actorTypeField: true, targetField: true, typeField: true, requestField: true,
}

// The audit query reads events e, the acting user u and the user an event is about, s.
const (
	actorValues  = "(SELECT unnest(ARRAY[u.name, u.email]))"
	targetValues = "(SELECT unnest(ARRAY[e.subject_id::text, e.data->>'name', e.data->>'domain', s.name, s.email]) " +
		"UNION ALL SELECT jsonb_array_elements_text(" +
		"CASE WHEN jsonb_typeof(e.data->'names') = 'array' THEN e.data->'names' END))"
	textColumns = "(SELECT unnest(ARRAY[e.type, u.name, u.email]))"
)

var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// ValidateQuery rejects fields an audit query does not have and number comparisons, which no audit
// field holds.
func ValidateQuery(expr logquery.Expr) error {
	switch expr := expr.(type) {
	case logquery.And:
		return validateQueries(expr.Terms)
	case logquery.Or:
		return validateQueries(expr.Terms)
	case logquery.Not:
		return ValidateQuery(expr.Term)
	case logquery.Field:
		if !queryFields[expr.Key] {
			return &logquery.SyntaxError{
				Position: expr.Position,
				Message: fmt.Sprintf(
					"unknown field @%s; use @actor, @actor_type, @target, @type or @request", expr.Key,
				),
			}
		}
		if expr.Match != logquery.Equal && expr.Match != logquery.Prefix {
			return &logquery.SyntaxError{
				Position: expr.Position,
				Message:  fmt.Sprintf("@%s does not compare numbers", expr.Key),
			}
		}
	}
	return nil
}

func validateQueries(terms []logquery.Expr) error {
	for _, term := range terms {
		if err := ValidateQuery(term); err != nil {
			return err
		}
	}
	return nil
}

// binder collects the arguments of a query and names each by its $n placeholder.
type binder struct{ args []any }

func (b *binder) bind(value any) string {
	b.args = append(b.args, value)
	return "$" + strconv.Itoa(len(b.args))
}

// querySQL compiles a validated audit query into a WHERE condition, binding every value through b.
// A term on a value an event lacks is NULL; negation reads NULL as false, so -@request:x keeps the
// events without a request ID. Terms outside a negation stay plain comparisons the indexes serve.
func querySQL(expr logquery.Expr, b *binder) string {
	switch expr := expr.(type) {
	case logquery.And:
		return joinQueries(expr.Terms, " AND ", b)
	case logquery.Or:
		return joinQueries(expr.Terms, " OR ", b)
	case logquery.Not:
		return "NOT coalesce(" + querySQL(expr.Term, b) + ", false)"
	case logquery.Text:
		pattern := b.bind("%" + likeEscaper.Replace(expr.Value) + "%")
		return "(EXISTS (SELECT 1 FROM " + textColumns + " t(v) WHERE t.v ILIKE " + pattern + ` ESCAPE '\') OR ` +
			"EXISTS (SELECT 1 FROM " + targetValues + " t(v) WHERE t.v ILIKE " + pattern + ` ESCAPE '\') OR ` +
			"e.request_id = " + b.bind(expr.Value) + ")"
	case logquery.Field:
		return fieldSQL(expr, b)
	}
	panic("unknown audit query expression")
}

func joinQueries(terms []logquery.Expr, operator string, b *binder) string {
	parts := make([]string, len(terms))
	for i, term := range terms {
		parts[i] = querySQL(term, b)
	}
	return "(" + strings.Join(parts, operator) + ")"
}

func fieldSQL(field logquery.Field, b *binder) string {
	id, idErr := uuid.Parse(field.Value)
	isID := idErr == nil && field.Match == logquery.Equal
	switch field.Key {
	case actorField:
		if isID {
			return "e.actor_id = " + b.bind(id)
		}
		return anyMatches(actorValues, field, b)
	case targetField:
		if isID {
			return "e.subject_id = " + b.bind(id)
		}
		return anyMatches(targetValues, field, b)
	case actorTypeField:
		return exactMatch("e.actor_type", field, b)
	case typeField:
		return exactMatch("e.type", field, b)
	case requestField:
		return exactMatch("e.request_id", field, b)
	}
	panic("unknown audit query field " + field.Key)
}

// anyMatches matches when one of values equals the field's value or starts with it, ignoring case.
func anyMatches(values string, field logquery.Field, b *binder) string {
	return "EXISTS (SELECT 1 FROM " + values + " t(v) WHERE t.v ILIKE " + b.bind(likePattern(field)) + ` ESCAPE '\')`
}

// exactMatch compares column with the field's value, or its prefix, keeping case.
func exactMatch(column string, field logquery.Field, b *binder) string {
	if field.Match == logquery.Prefix {
		return column + " LIKE " + b.bind(likePattern(field)) + ` ESCAPE '\'`
	}
	return column + " = " + b.bind(field.Value)
}

func likePattern(field logquery.Field) string {
	pattern := likeEscaper.Replace(field.Value)
	if field.Match == logquery.Prefix {
		pattern += "%"
	}
	return pattern
}
