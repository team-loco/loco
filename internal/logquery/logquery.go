// Package logquery parses the query language: free text, @key:value fields, AND, OR, - negation and
// parentheses. Callers compile the expressions for their own store and give keys their meaning.
package logquery

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// MaxLength bounds a query in bytes.
	MaxLength = 4096
	maxTerms  = 100
	maxDepth  = 16
)

// Expr is a parsed query: And, Or, Not, Text or Field.
type Expr interface{ expr() }

// And matches when every term matches.
type And struct{ Terms []Expr }

// Or matches when any term matches.
type Or struct{ Terms []Expr }

// Not matches when its term does not.
type Not struct{ Term Expr }

// Text matches a record whose text contains Value.
type Text struct{ Value string }

// Match is how a Field compares its key's value.
type Match int

const (
	Equal Match = iota
	Prefix
	Greater
	GreaterOrEqual
	Less
	LessOrEqual
	Between
)

// Field matches a record whose Key holds Value (Equal, Prefix) or a number compared with Low and
// High. Position is where its @ stands, counted in characters from 1.
type Field struct {
	Key       string
	Match     Match
	Value     string
	Low, High float64
	Position  int
}

func (And) expr()   {}
func (Or) expr()    {}
func (Not) expr()   {}
func (Text) expr()  {}
func (Field) expr() {}

// Quote writes value as a quoted phrase or field value that matches it exactly.
func Quote(value string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(value) + `"`
}

// Uses reports whether expr has a field named key.
func Uses(expr Expr, key string) bool {
	switch expr := expr.(type) {
	case And:
		return slices.ContainsFunc(expr.Terms, func(term Expr) bool { return Uses(term, key) })
	case Or:
		return slices.ContainsFunc(expr.Terms, func(term Expr) bool { return Uses(term, key) })
	case Not:
		return Uses(expr.Term, key)
	case Field:
		return expr.Key == key
	}
	return false
}

// SyntaxError is a query the parser rejects; Position counts characters from 1.
type SyntaxError struct {
	Position int
	Message  string
}

// keep in sync with QUERY_ERROR_RE in web/src/pages/observability/query.ts
func (e *SyntaxError) Error() string {
	return fmt.Sprintf("%s at position %d", e.Message, e.Position)
}

// Parse parses query, returning nil for an empty one.
func Parse(query string) (Expr, error) {
	if len(query) > MaxLength {
		return nil, &SyntaxError{Position: 1, Message: fmt.Sprintf("query is longer than %d bytes", MaxLength)}
	}
	if !utf8.ValidString(query) {
		return nil, &SyntaxError{Position: 1, Message: "query is not valid UTF-8"}
	}
	p := &parser{src: []rune(query)}
	p.space()
	if p.end() {
		return nil, nil
	}
	expr, err := p.or()
	if err != nil {
		return nil, err
	}
	if !p.end() {
		return nil, p.errorf("unexpected %q", string(p.peek()))
	}
	return expr, nil
}

type parser struct {
	src   []rune
	pos   int
	terms int
	depth int
}

func (p *parser) or() (Expr, error) {
	first, err := p.and()
	if err != nil {
		return nil, err
	}
	terms := []Expr{first}
	for p.keyword("OR") {
		next, err := p.and()
		if err != nil {
			return nil, err
		}
		terms = append(terms, next)
	}
	if len(terms) == 1 {
		return first, nil
	}
	return Or{Terms: terms}, nil
}

func (p *parser) and() (Expr, error) {
	var terms []Expr
	for {
		if len(terms) > 0 {
			p.keyword("AND")
		}
		term, err := p.unary()
		if err != nil {
			return nil, err
		}
		terms = append(terms, term)
		p.space()
		if p.end() || p.peek() == ')' || p.atKeyword("OR") {
			break
		}
	}
	if len(terms) == 1 {
		return terms[0], nil
	}
	return And{Terms: terms}, nil
}

func (p *parser) unary() (Expr, error) {
	p.space()
	if p.end() || p.peek() == ')' || p.atKeyword("AND") || p.atKeyword("OR") {
		return nil, p.errorf("expected a term")
	}
	if p.peek() != '-' {
		return p.primary()
	}
	p.pos++
	if p.end() || unicode.IsSpace(p.peek()) {
		return nil, p.errorf("expected a term after -")
	}
	if err := p.enter(); err != nil {
		return nil, err
	}
	term, err := p.unary()
	p.depth--
	if err != nil {
		return nil, err
	}
	return Not{Term: term}, nil
}

func (p *parser) primary() (Expr, error) {
	switch p.peek() {
	case '(':
		open := p.pos
		p.pos++
		if err := p.enter(); err != nil {
			return nil, err
		}
		expr, err := p.or()
		p.depth--
		if err != nil {
			return nil, err
		}
		if p.end() || p.peek() != ')' {
			return nil, &SyntaxError{Position: open + 1, Message: "unclosed ("}
		}
		p.pos++
		return expr, nil
	case '@':
		return p.field()
	case '"':
		start := p.pos
		phrase, err := p.quoted()
		if err != nil {
			return nil, err
		}
		if phrase == "" {
			return nil, &SyntaxError{Position: start + 1, Message: "empty phrase"}
		}
		return p.term(Text{Value: phrase})
	default:
		return p.term(Text{Value: p.word()})
	}
}

func (p *parser) field() (Expr, error) {
	start := p.pos
	p.pos++
	key, err := p.key()
	if err != nil {
		return nil, err
	}
	if key == "" {
		return nil, p.errorf("expected a key after @")
	}
	if p.end() || p.peek() != ':' {
		return nil, p.errorf("expected : after @%s", key)
	}
	p.pos++
	field := Field{Key: key, Position: start + 1}
	switch {
	case p.end() || unicode.IsSpace(p.peek()) || p.peek() == ')':
		return nil, p.errorf("expected a value after @%s:", key)
	case p.peek() == '"':
		if field.Value, err = p.quoted(); err != nil {
			return nil, err
		}
	case p.peek() == '>' || p.peek() == '<':
		if err := p.comparison(&field); err != nil {
			return nil, err
		}
	default:
		if err := p.bareValue(&field); err != nil {
			return nil, err
		}
	}
	return p.term(field)
}

func (p *parser) key() (string, error) {
	var key strings.Builder
	for !p.end() {
		r := p.peek()
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("_.-/", r):
			key.WriteRune(r)
			p.pos++
		case r == '[':
			open := p.pos
			p.pos++
			digits := p.run(unicode.IsDigit)
			if digits == "" || p.end() || p.peek() != ']' {
				return "", &SyntaxError{Position: open + 1, Message: "expected [index] with a number"}
			}
			p.pos++
			if key.Len() == 0 {
				return "", &SyntaxError{Position: open + 1, Message: "expected a key before ["}
			}
			key.WriteString("." + digits)
		default:
			return key.String(), nil
		}
	}
	return key.String(), nil
}

func (p *parser) comparison(field *Field) error {
	operator := string(p.peek())
	p.pos++
	if !p.end() && p.peek() == '=' {
		operator += "="
		p.pos++
	}
	field.Match = map[string]Match{">": Greater, ">=": GreaterOrEqual, "<": Less, "<=": LessOrEqual}[operator]
	start := p.pos
	number, err := number(p.bare(), start)
	field.Low, field.High = number, number
	return err
}

func (p *parser) bareValue(field *Field) error {
	start := p.pos
	value := p.bare()
	switch {
	case strings.ContainsRune(value, '"'):
		return &SyntaxError{Position: start + 1, Message: "quote the whole value"}
	case strings.ContainsRune(value, ':'):
		return &SyntaxError{Position: start + 1, Message: "quote values that contain :"}
	case strings.Contains(value, ".."):
		low, high, _ := strings.Cut(value, "..")
		var lowErr, highErr error
		field.Low, lowErr = number(low, start)
		field.High, highErr = number(high, start)
		if lowErr != nil || highErr != nil {
			return &SyntaxError{Position: start + 1, Message: "write a range as 1..9, or quote values that contain .."}
		}
		if field.Low > field.High {
			return &SyntaxError{Position: start + 1, Message: "range starts above its end"}
		}
		field.Match = Between
		return nil
	}
	if star := strings.IndexRune(value, '*'); star >= 0 {
		if star != len(value)-1 || star == 0 {
			return &SyntaxError{
				Position: start + utf8.RuneCountInString(value[:star]) + 1,
				Message:  "* only ends a prefix, as in /api/*",
			}
		}
		field.Match, field.Value = Prefix, value[:star]
		return nil
	}
	field.Value = value
	return nil
}

func number(text string, start int) (float64, error) {
	number, err := strconv.ParseFloat(text, 64)
	if err != nil || math.IsNaN(number) || math.IsInf(number, 0) {
		return 0, &SyntaxError{Position: start + 1, Message: fmt.Sprintf("expected a number, got %q", text)}
	}
	return number, nil
}

// quoted reads a "..." string with \" and \\ escapes.
func (p *parser) quoted() (string, error) {
	open := p.pos
	p.pos++
	var value strings.Builder
	for !p.end() {
		r := p.peek()
		p.pos++
		switch r {
		case '"':
			return value.String(), nil
		case '\\':
			if p.end() || (p.peek() != '"' && p.peek() != '\\') {
				return "", p.errorf(`expected \" or \\ after \`)
			}
			value.WriteRune(p.peek())
			p.pos++
		default:
			value.WriteRune(r)
		}
	}
	return "", &SyntaxError{Position: open + 1, Message: "unclosed quote"}
}

func (p *parser) word() string {
	return p.run(func(r rune) bool { return !unicode.IsSpace(r) && r != '(' && r != ')' && r != '"' })
}

func (p *parser) bare() string {
	return p.run(func(r rune) bool { return !unicode.IsSpace(r) && r != '(' && r != ')' })
}

func (p *parser) run(keep func(rune) bool) string {
	start := p.pos
	for !p.end() && keep(p.peek()) {
		p.pos++
	}
	return string(p.src[start:p.pos])
}

func (p *parser) term(expr Expr) (Expr, error) {
	p.terms++
	if p.terms > maxTerms {
		return nil, p.errorf("query has more than %d terms", maxTerms)
	}
	return expr, nil
}

func (p *parser) enter() error {
	p.depth++
	if p.depth > maxDepth {
		return p.errorf("query nests deeper than %d levels", maxDepth)
	}
	return nil
}

// keyword consumes word when it stands alone at the current position.
func (p *parser) keyword(word string) bool {
	p.space()
	if !p.atKeyword(word) {
		return false
	}
	p.pos += len(word)
	return true
}

func (p *parser) atKeyword(word string) bool {
	end := p.pos + len(word)
	if end > len(p.src) || string(p.src[p.pos:end]) != word {
		return false
	}
	return end == len(p.src) || unicode.IsSpace(p.src[end]) || p.src[end] == '('
}

func (p *parser) space() {
	for !p.end() && unicode.IsSpace(p.peek()) {
		p.pos++
	}
}

func (p *parser) end() bool  { return p.pos >= len(p.src) }
func (p *parser) peek() rune { return p.src[p.pos] }

func (p *parser) errorf(format string, args ...any) *SyntaxError {
	return &SyntaxError{Position: p.pos + 1, Message: fmt.Sprintf(format, args...)}
}
