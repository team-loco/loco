package dotenv

import (
	"errors"
	"fmt"
	"maps"
	"strings"
	"testing"
)

const longValueBytes = 70 * 1024

func TestParse(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  map[string]string
	}{
		{name: "empty", input: "", want: map[string]string{}},
		{name: "bare", input: "A=1\nB=two words\n", want: map[string]string{"A": "1", "B": "two words"}},
		{name: "no trailing newline", input: "A=1", want: map[string]string{"A": "1"}},
		{name: "crlf", input: "A=1\r\nB=2\r\n", want: map[string]string{"A": "1", "B": "2"}},
		{
			name:  "comments and blanks",
			input: "# header\n\n  \nA=1\n  # indented\n",
			want:  map[string]string{"A": "1"},
		},
		{name: "spaces around separator", input: "A = 1 \n", want: map[string]string{"A": "1"}},
		{name: "export prefix", input: "export A=1\n", want: map[string]string{"A": "1"}},
		{name: "empty value", input: "A=\nB=''\nC=\"\"\n", want: map[string]string{"A": "", "B": "", "C": ""}},
		{name: "last wins", input: "A=1\nA=2\n", want: map[string]string{"A": "2"}},
		{name: "value with separator", input: "A=b=c\n", want: map[string]string{"A": "b=c"}},
		{name: "inline comment", input: "A=1 # note\nB=1#tag\n", want: map[string]string{"A": "1", "B": "1#tag"}},
		{name: "comment after empty value", input: "EMPTY= # left unset\n", want: map[string]string{"EMPTY": ""}},
		{name: "comment after tab", input: "EMPTY=\t# left unset\n", want: map[string]string{"EMPTY": ""}},
		{name: "hash starts a bare value", input: "TAG=#tag\n", want: map[string]string{"TAG": "#tag"}},
		{name: "single quotes literal", input: `A='x "y" \n $B #z'`, want: map[string]string{"A": `x "y" \n $B #z`}},
		{
			name:  "double quotes escapes",
			input: `A="l1\nl2 \"q\" \\ \$B #z"`,
			want:  map[string]string{"A": "l1\nl2 \"q\" \\ $B #z"},
		},
		{name: "quoted then comment", input: `A="1" # note`, want: map[string]string{"A": "1"}},
		{name: "quote inside bare", input: `A=it's`, want: map[string]string{"A": "it's"}},
		{
			name:  "no interpolation",
			input: "A=1\nB=$A\nC=${A}\nD=\"$A ${A}\"\n",
			want:  map[string]string{"A": "1", "B": "$A", "C": "${A}", "D": "$A ${A}"},
		},
		{name: "uppercase and digits", input: "A_1=x\n_B=y\n", want: map[string]string{"A_1": "x", "_B": "y"}},
		{
			name:  "unknown escapes kept",
			input: `A="a\tb\xc\\d"`,
			want:  map[string]string{"A": `a\tb\xc\d`},
		},
		{
			name:  "multi-line double quotes",
			input: "PRIVATE_KEY=\"-----BEGIN KEY-----\r\nabc  \ndef\n-----END KEY-----\" # pem\nB=2\n",
			want: map[string]string{
				"PRIVATE_KEY": "-----BEGIN KEY-----\nabc  \ndef\n-----END KEY-----",
				"B":           "2",
			},
		},
		{
			name:  "line longer than the default scanner token",
			input: "A=" + strings.Repeat("x", longValueBytes) + "\n",
			want:  map[string]string{"A": strings.Repeat("x", longValueBytes)},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := strings.NewReader(tt.input)
			got, err := Parse(reader)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if !maps.Equal(got, tt.want) {
				t.Errorf("Parse = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  error
		line  int
	}{
		{name: "missing separator", input: "A=1\nB\n", want: ErrMissingSeparator, line: 2},
		{name: "empty name", input: "=1\n", want: ErrEmptyName, line: 1},
		{name: "leading digit", input: "1A=x\n", want: ErrInvalidName, line: 1},
		{name: "dash in name", input: "A-B=x\n", want: ErrInvalidName, line: 1},
		{name: "space in name", input: "A B=x\n", want: ErrInvalidName, line: 1},
		{name: "unterminated double", input: "A=\"secret\n", want: ErrUnterminated, line: 1},
		{name: "unterminated single", input: "\n\nA='secret\n", want: ErrUnterminated, line: 3},
		{name: "unterminated multi-line", input: "\nA=\"secret\nmore\n\nB=1\n", want: ErrUnterminated, line: 2},
		{name: "lowercase name", input: "db_url=x\n", want: ErrInvalidName, line: 1},
		{name: "mixed case name", input: "Db=x\n", want: ErrInvalidName, line: 1},
		{name: "escaped closing quote", input: "A=\"x\\\"\n", want: ErrUnterminated, line: 1},
		{name: "trailing text", input: "A='x' y\n", want: ErrTrailingText, line: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := strings.NewReader(tt.input)
			got, err := Parse(reader)
			if !errors.Is(err, tt.want) {
				t.Fatalf("Parse error = %v, want %v", err, tt.want)
			}
			if got != nil {
				t.Errorf("Parse returned %#v with an error", got)
			}
			message := err.Error()
			prefix := fmt.Sprintf("line %d: ", tt.line)
			if !strings.HasPrefix(message, prefix) {
				t.Errorf("error %q does not start with %q", message, prefix)
			}
			if strings.Contains(message, "secret") {
				t.Errorf("error %q carries line contents", message)
			}
		})
	}
}

type failingReader struct{}

var errRead = errors.New("disk gone")

func (failingReader) Read([]byte) (int, error) {
	return 0, errRead
}

func TestParseReadError(t *testing.T) {
	_, err := Parse(failingReader{})
	if !errors.Is(err, errRead) {
		t.Fatalf("Parse error = %v, want %v", err, errRead)
	}
}
