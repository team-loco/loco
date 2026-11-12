package mail

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"
)

const (
	testHookURL = "https://hooks.loco.test/mail"
	testHTTPURL = "http://receiver.loco.test:8080/mail"
	testSMTPURL = "smtp://mailer@mail.loco.test:587"

	testTimeout  = 10 * time.Second
	stallTimeout = 200 * time.Millisecond
)

func testHookSecret() string {
	return "whsec_" + base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))
}

func TestParseTransport(t *testing.T) {
	tests := []struct {
		name   string
		raw    string
		secret string
		want   error
	}{
		{name: "empty", raw: "", secret: ""},
		{name: "smtp rejected", raw: testSMTPURL, want: ErrUnsupportedScheme},
		{name: "hook", raw: testHookURL, secret: testHookSecret()},
		{name: "hook over http", raw: testHTTPURL, secret: testHookSecret()},
		{name: "secret missing", raw: testHookURL, want: ErrHookSecret},
		{name: "http secret missing", raw: testHTTPURL, want: ErrHookSecret},
		{name: "secret malformed", raw: testHookURL, secret: "plain", want: ErrHookSecret},
		{name: "scheme", raw: "ftp://mail.loco.test", want: ErrUnsupportedScheme},
		{name: "unparsable", raw: "https://user:hunter2@[::1", want: ErrInvalidTransport},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			transport, err := ParseTransport(tt.raw, tt.secret, testTimeout)
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
	transport, err := ParseTransport("", "", testTimeout)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := transport.Send(t.Context(), Message{}); !errors.Is(err, ErrNoTransport) {
		t.Fatalf("send = %v, want ErrNoTransport", err)
	}
}
