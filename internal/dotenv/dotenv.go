// Package dotenv parses KEY=VALUE files in the .env format without expanding references.
package dotenv

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"
)

var (
	ErrMissingSeparator = errors.New("missing \"=\"")
	ErrEmptyName        = errors.New("empty name")
	ErrInvalidName      = errors.New("name must match ^[A-Z_][A-Z0-9_]*$")
	ErrUnterminated     = errors.New("unterminated quote")
	ErrTrailingText     = errors.New("text after the closing quote")

	errOpenDoubleQuote = errors.New("double quote continues on the next line")
)

const (
	exportPrefix = "export "
	maxLineBytes = 1 << 20
)

// Parse reads KEY=VALUE lines and returns the values by name; a later line replaces an earlier one.
// Blank lines and lines starting with # are skipped, an optional export prefix is dropped,
// and a value may be single-quoted, double-quoted or bare. Names match ^[A-Z_][A-Z0-9_]*$, the
// rule the API enforces. A double-quoted value may span lines and keeps their newlines; it turns
// the \n, \", \\ and \$ escapes into their characters and keeps any other backslash as written.
// Bare values end at a # that follows whitespace, so a value of only whitespace and a comment is
// empty while a value starting with # is literal. Nothing is expanded, so $ and ${NAME} stay
// literal. Lines may be up to 1 MiB. Errors name the line and never carry its contents.
func Parse(r io.Reader) (map[string]string, error) {
	values := map[string]string{}
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, bufio.MaxScanTokenSize), maxLineBytes)
	lineNumber := 0
	startLine := 0
	pending := ""
	for scanner.Scan() {
		lineNumber++
		raw := strings.TrimSuffix(scanner.Text(), "\r")
		if pending != "" {
			pending += "\n" + raw
		} else {
			line := strings.TrimSpace(raw)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			pending = raw
			startLine = lineNumber
		}
		name, value, err := parseLine(pending)
		if errors.Is(err, errOpenDoubleQuote) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", startLine, err)
		}
		values[name] = value
		pending = ""
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read .env: %w", err)
	}
	if pending != "" {
		return nil, fmt.Errorf("line %d: %w", startLine, ErrUnterminated)
	}
	return values, nil
}

func parseLine(line string) (string, string, error) {
	line = strings.TrimPrefix(strings.TrimSpace(line), exportPrefix)
	name, rest, found := strings.Cut(line, "=")
	if !found {
		return "", "", ErrMissingSeparator
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return "", "", ErrEmptyName
	}
	if !isIdentifier(name) {
		return "", "", ErrInvalidName
	}
	value, err := parseValue(rest)
	if err != nil {
		return "", "", err
	}
	return name, value, nil
}

func parseValue(rest string) (string, error) {
	raw := strings.TrimSpace(rest)
	if raw == "" {
		return "", nil
	}
	if raw[0] == '#' && isSpace(rest[0]) {
		return "", nil
	}
	switch raw[0] {
	case '\'':
		return parseQuoted(raw, '\'', false)
	case '"':
		return parseQuoted(raw, '"', true)
	default:
		return parseBare(raw), nil
	}
}

func parseBare(raw string) string {
	for i := 1; i < len(raw); i++ {
		if raw[i] == '#' && isSpace(raw[i-1]) {
			return strings.TrimSpace(raw[:i])
		}
	}
	return raw
}

func parseQuoted(raw string, quote byte, escapes bool) (string, error) {
	var out strings.Builder
	for i := 1; i < len(raw); i++ {
		c := raw[i]
		switch {
		case escapes && c == '\\' && i+1 < len(raw):
			i++
			writeEscape(&out, raw[i])
		case c == quote:
			return out.String(), checkTrailing(raw[i+1:])
		default:
			out.WriteByte(c)
		}
	}
	if escapes {
		return "", errOpenDoubleQuote
	}
	return "", ErrUnterminated
}

func writeEscape(out *strings.Builder, c byte) {
	switch c {
	case 'n':
		out.WriteByte('\n')
	case '"':
		out.WriteByte(c)
	case '\\':
		out.WriteByte(c)
	case '$':
		out.WriteByte(c)
	default:
		out.WriteByte('\\')
		out.WriteByte(c)
	}
}

func checkTrailing(rest string) error {
	rest = strings.TrimSpace(rest)
	if rest == "" || strings.HasPrefix(rest, "#") {
		return nil
	}
	return ErrTrailingText
}

func isIdentifier(name string) bool {
	for i := range len(name) {
		c := name[i]
		letter := c == '_' || (c >= 'A' && c <= 'Z')
		digit := c >= '0' && c <= '9' && i > 0
		if !letter && !digit {
			return false
		}
	}
	return true
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t'
}
