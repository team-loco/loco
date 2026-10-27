package mail

import (
	"strings"
	"testing"

	"github.com/team-loco/loco/api/mail/mailtest"
)

func newTestSMTP(t *testing.T, server *mailtest.SMTPServer, password string) Transport {
	t.Helper()
	transport, err := ParseTransport(server.URL(password), testFrom, "", WithTLSConfig(server.ClientTLS()))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return transport
}

func TestSMTPDeliversOverSTARTTLS(t *testing.T) {
	server := mailtest.NewSMTPServer(t, true)
	transport := newTestSMTP(t, server, mailtest.Password)
	event := testEvent()

	if err := transport.Send(t.Context(), NewInvitationMessage(event)); err != nil {
		t.Fatalf("send: %v", err)
	}

	got := <-server.Captured
	if got.AuthLine == "" {
		t.Error("client never authenticated")
	}
	if got.From != "FROM:<noreply@loco.test>" || got.Rcpt != "TO:<bob@acme.test>" {
		t.Errorf("envelope from = %q rcpt = %q", got.From, got.Rcpt)
	}
	for _, want := range []string{
		"From: \"Loco\" <noreply@loco.test>\r\n",
		"To: <bob@acme.test>\r\n",
		"Reply-To: <alice@acme.test>\r\n",
		"Subject: You have been invited to join Acme on Loco\r\n",
		"Content-Type: text/plain; charset=utf-8\r\n",
		"Alice (alice@acme.test) invited you to join Acme on Loco.",
		event.Link,
	} {
		if !strings.Contains(got.Data, want) {
			t.Errorf("message lacks %q:\n%s", want, got.Data)
		}
	}
}

func TestSMTPAuthFailureSurfaces(t *testing.T) {
	server := mailtest.NewSMTPServer(t, true)
	transport := newTestSMTP(t, server, "wrong")

	err := transport.Send(t.Context(), NewInvitationMessage(testEvent()))
	if err == nil || !strings.Contains(err.Error(), "smtp auth") {
		t.Fatalf("err = %v, want an auth failure", err)
	}
}

func TestSMTPRequiresSTARTTLS(t *testing.T) {
	server := mailtest.NewSMTPServer(t, false)
	transport := newTestSMTP(t, server, mailtest.Password)

	err := transport.Send(t.Context(), NewInvitationMessage(testEvent()))
	if err == nil || !strings.Contains(err.Error(), ErrSMTPNoSTARTTLS.Error()) {
		t.Fatalf("err = %v, want ErrSMTPNoSTARTTLS", err)
	}
}
