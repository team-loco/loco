package logquery

import (
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

const (
	expectedTerm = "expected a term"
	starNotAtEnd = "* only ends a prefix"
	statusKey    = "status"
)

func TestParse(t *testing.T) {
	eq := func(key, value string) Field { return Field{Key: key, Value: value} }
	for _, tc := range []struct {
		query string
		want  Expr
	}{
		{"", nil},
		{"   ", nil},
		{"timeout", Text{Value: "timeout"}},
		{`"payment failed"`, Text{Value: "payment failed"}},
		{`"say \"hi\" \\ bye"`, Text{Value: `say "hi" \ bye`}},
		{"café 東京", And{Terms: []Expr{Text{Value: "café"}, Text{Value: "東京"}}}},
		{"@level:error", eq("level", "error")},
		{`@path:"/a:b"`, eq("path", "/a:b")},
		{"@path:/api/v1", eq("path", "/api/v1")},
		{"@path:/api/*", Field{Key: "path", Match: Prefix, Value: "/api/"}},
		{`@path:"/api/*"`, eq("path", "/api/*")},
		{"@http.method:GET", eq("http.method", "GET")},
		{"@loco.io/build-id:b1", eq("loco.io/build-id", "b1")},
		{"@roles[0]:admin", eq("roles.0", "admin")},
		{"@matrix[1][2]:x", eq("matrix.1.2", "x")},
		{"@User.ID:7", eq("User.ID", "7")},
		{"@status:>499", Field{Key: statusKey, Match: Greater, Low: 499, High: 499}},
		{"@status:>=500", Field{Key: statusKey, Match: GreaterOrEqual, Low: 500, High: 500}},
		{"@ms:<1.5", Field{Key: "ms", Match: Less, Low: 1.5, High: 1.5}},
		{"@ms:<=-2", Field{Key: "ms", Match: LessOrEqual, Low: -2, High: -2}},
		{"@status:200..299", Field{Key: statusKey, Match: Between, Low: 200, High: 299}},
		{"-@level:debug", Not{Term: eq("level", "debug")}},
		{"a b OR c", Or{Terms: []Expr{And{Terms: []Expr{Text{Value: "a"}, Text{Value: "b"}}}, Text{Value: "c"}}}},
		{"a AND b OR c AND d", Or{Terms: []Expr{
			And{Terms: []Expr{Text{Value: "a"}, Text{Value: "b"}}},
			And{Terms: []Expr{Text{Value: "c"}, Text{Value: "d"}}},
		}}},
		{"a (b OR c)", And{Terms: []Expr{Text{Value: "a"}, Or{Terms: []Expr{Text{Value: "b"}, Text{Value: "c"}}}}}},
		{"-(a OR b)", Not{Term: Or{Terms: []Expr{Text{Value: "a"}, Text{Value: "b"}}}}},
		{"a or b and c", And{Terms: []Expr{
			Text{Value: "a"}, Text{Value: "or"}, Text{Value: "b"}, Text{Value: "and"}, Text{Value: "c"},
		}}},
		{"ORDER ANDROID", And{Terms: []Expr{Text{Value: "ORDER"}, Text{Value: "ANDROID"}}}},
		{"a OR(b)", Or{Terms: []Expr{Text{Value: "a"}, Text{Value: "b"}}}},
		{"key:value", Text{Value: "key:value"}},
		{"user@example.com", Text{Value: "user@example.com"}},
		{"--a", Not{Term: Not{Term: Text{Value: "a"}}}},
	} {
		got, err := Parse(tc.query)
		if err != nil {
			t.Errorf("Parse(%q) error: %v", tc.query, err)
			continue
		}
		if !reflect.DeepEqual(withoutPositions(got), tc.want) {
			t.Errorf("Parse(%q) = %#v, want %#v", tc.query, got, tc.want)
		}
	}
}

func TestParseRejects(t *testing.T) {
	for _, tc := range []struct {
		query    string
		position int
		message  string
	}{
		{"(a OR b", 1, "unclosed ("},
		{"a OR b)", 7, `unexpected ")"`},
		{")", 1, expectedTerm},
		{"()", 2, expectedTerm},
		{"a OR", 5, expectedTerm},
		{"OR a", 1, expectedTerm},
		{"AND a", 1, expectedTerm},
		{"a AND OR b", 7, expectedTerm},
		{"- a", 2, "expected a term after -"},
		{`"open`, 1, "unclosed quote"},
		{`""`, 1, "empty phrase"},
		{`"bad \n escape"`, 7, `expected \" or \\ after \`},
		{"@", 2, "expected a key after @"},
		{"@level", 7, "expected : after @level"},
		{"@level:", 8, "expected a value after @level:"},
		{"@path:/a:b", 7, "quote values that contain :"},
		{`@path:a"b`, 7, "quote the whole value"},
		{"@path:*api", 7, starNotAtEnd},
		{"@path:/a*b", 9, starNotAtEnd},
		{"@path:*", 7, starNotAtEnd},
		{"@status:>abc", 10, `expected a number, got "abc"`},
		{"@status:>", 10, `expected a number, got ""`},
		{"@status:>NaN", 10, "expected a number"},
		{"@status:1..x", 12, `expected a number, got "x"`},
		{"@status:..5", 9, `expected a number, got ""`},
		{"@status:9..1", 9, "range starts above its end"},
		{"@roles[]:x", 7, "expected [index] with a number"},
		{"@[0]:x", 2, "expected a key before ["},
		{strings.Repeat("(", maxDepth+1) + "a" + strings.Repeat(")", maxDepth+1), maxDepth + 2, "nests deeper than"},
		{strings.Repeat("-", maxDepth+1) + "a", maxDepth + 2, "nests deeper than"},
		{strings.Repeat("a ", maxTerms+1), 2*maxTerms + 2, "more than"},
		{strings.Repeat("a", MaxLength+1), 1, "longer than"},
		{"\xff", 1, "not valid UTF-8"},
	} {
		_, err := Parse(tc.query)
		var syntax *SyntaxError
		if !errors.As(err, &syntax) {
			t.Errorf("Parse(%q) error = %v, want a SyntaxError", tc.query, err)
			continue
		}
		if syntax.Position != tc.position || !strings.Contains(syntax.Message, tc.message) {
			t.Errorf(
				"Parse(%q) = %q at %d, want %q at %d",
				tc.query,
				syntax.Message,
				syntax.Position,
				tc.message,
				tc.position,
			)
		}
	}
}

func TestQuoteRoundTrips(t *testing.T) {
	for _, value := range []string{"plain", "with space", `say "hi"`, `back\slash`, "/a:b", "-neg", "a*", "1..2", "é 東京"} {
		got, err := Parse("@k:" + Quote(value) + " " + Quote(value))
		want := And{Terms: []Expr{Field{Key: "k", Value: value}, Text{Value: value}}}
		if err != nil || !reflect.DeepEqual(withoutPositions(got), want) {
			t.Errorf("Quote(%q) parses to %#v, %v", value, got, err)
		}
	}
}

func FuzzParse(f *testing.F) {
	for _, seed := range []string{
		"", "a b OR c", `@path:"/a:b"`, "-(@level:error OR @level:warn) timeout", "@status:200..299",
		"@roles[0]:admin", "@ms:>=1.5 @path:/api/*", `"quoted \"phrase\""`, "(((a)))", "a AND -b",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, query string) {
		expr, err := Parse(query)
		var syntax *SyntaxError
		if err != nil {
			if !errors.As(err, &syntax) {
				t.Fatalf("Parse(%q) error %v is not a SyntaxError", query, err)
			}
			if syntax.Position < 1 || syntax.Position > utf8.RuneCountInString(query)+1 {
				t.Fatalf("Parse(%q) position %d is outside the query", query, syntax.Position)
			}
			return
		}
		if expr == nil {
			return
		}
		formatted := format(expr)
		again, err := Parse(formatted)
		if err != nil {
			if errors.As(err, &syntax) &&
				(strings.Contains(syntax.Message, "nests deeper") || strings.Contains(syntax.Message, "longer than")) {
				return
			}
			t.Fatalf("Parse(%q) of the formatted %q: %v", formatted, query, err)
		}
		if !reflect.DeepEqual(withoutPositions(again), withoutPositions(expr)) {
			t.Fatalf("Parse(%q) = %#v, want %#v from %q", formatted, again, expr, query)
		}
	})
}

// format prints expr so that Parse returns it again.
func format(expr Expr) string {
	number := func(value float64) string { return strconv.FormatFloat(value, 'g', -1, 64) }
	join := func(terms []Expr, separator string) string {
		parts := make([]string, len(terms))
		for i, term := range terms {
			parts[i] = format(term)
		}
		return "(" + strings.Join(parts, separator) + ")"
	}
	switch expr := expr.(type) {
	case And:
		return join(expr.Terms, " ")
	case Or:
		return join(expr.Terms, " OR ")
	case Not:
		return "-" + format(expr.Term)
	case Text:
		return Quote(expr.Value)
	case Field:
		value := map[Match]string{
			Equal:          Quote(expr.Value),
			Prefix:         expr.Value + "*",
			Greater:        ">" + number(expr.Low),
			GreaterOrEqual: ">=" + number(expr.Low),
			Less:           "<" + number(expr.Low),
			LessOrEqual:    "<=" + number(expr.Low),
			Between:        number(expr.Low) + ".." + number(expr.High),
		}[expr.Match]
		return "@" + expr.Key + ":" + value
	}
	panic("unknown expression")
}

// withoutPositions clears every Field's position, which formatting moves.
func withoutPositions(expr Expr) Expr {
	strip := func(terms []Expr) []Expr {
		cleared := make([]Expr, len(terms))
		for i, term := range terms {
			cleared[i] = withoutPositions(term)
		}
		return cleared
	}
	switch expr := expr.(type) {
	case And:
		return And{Terms: strip(expr.Terms)}
	case Or:
		return Or{Terms: strip(expr.Terms)}
	case Not:
		return Not{Term: withoutPositions(expr.Term)}
	case Field:
		expr.Position = 0
		return expr
	}
	return expr
}

func TestFieldPositionCountsCharacters(t *testing.T) {
	expr, err := Parse(`é (a OR -@k:v)`)
	if err != nil {
		t.Fatal(err)
	}
	field, ok := expr.(And).Terms[1].(Or).Terms[1].(Not).Term.(Field)
	if !ok || field.Position != 10 {
		t.Fatalf("position = %d, want 10", field.Position)
	}
}
