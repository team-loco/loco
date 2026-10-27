package mail

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

const (
	testFrom    = "Loco <noreply@loco.test>"
	testHookURL = "https://hooks.loco.test/mail"
	testSMTPURL = "smtp://mailer@mail.loco.test:587"
)

func testHookSecret() string {
	return "whsec_" + base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))
}

func TestParseTransport(t *testing.T) {
	tests := []struct {
		name   string
		raw    string
		from   string
		secret string
		want   error
	}{
		{name: "empty", raw: "", from: "", secret: ""},
		{name: "smtp", raw: testSMTPURL, from: testFrom},
		{name: "hook", raw: testHookURL, from: testFrom, secret: testHookSecret()},
		{name: "from missing", raw: "smtp://mail.loco.test:587", want: ErrFromRequired},
		{name: "from malformed", raw: "smtp://mail.loco.test:587", from: "not an address", want: ErrInvalidFrom},
		{name: "smtp without port", raw: "smtp://mail.loco.test", from: testFrom, want: ErrInvalidTransport},
		{name: "secret missing", raw: testHookURL, from: testFrom, want: ErrHookSecret},
		{name: "secret malformed", raw: testHookURL, from: testFrom, secret: "plain", want: ErrHookSecret},
		{name: "scheme", raw: "ftp://mail.loco.test", from: testFrom, want: ErrUnsupportedScheme},
		{name: "unparsable", raw: "smtp://user:hunter2@[::1", from: testFrom, want: ErrInvalidTransport},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transport, err := ParseTransport(tt.raw, tt.from, tt.secret)
			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			if tt.want == nil && transport == nil {
				t.Fatal("no transport")
			}
			if err != nil && strings.Contains(err.Error(), "hunter2") {
				t.Fatalf("error leaks the password: %v", err)
			}
		})
	}
}

func TestNoTransportReportsItself(t *testing.T) {
	transport, err := ParseTransport("", "", "")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := transport.Send(t.Context(), Message{}); !errors.Is(err, ErrNoTransport) {
		t.Fatalf("send = %v, want ErrNoTransport", err)
	}
}
