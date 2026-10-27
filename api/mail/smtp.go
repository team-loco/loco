package mail

import (
	"context"
	"crypto/tls"
	"fmt"
	"mime"
	"net"
	netmail "net/mail"
	"net/smtp"
	"net/url"
	"strings"
	"time"
)

const smtpTimeout = 30 * time.Second

type smtpTransport struct {
	addr      string
	host      string
	auth      smtp.Auth
	from      *netmail.Address
	tlsConfig *tls.Config
}

func newSMTP(u *url.URL, from *netmail.Address, tlsConfig *tls.Config) (*smtpTransport, error) {
	host := u.Hostname()
	if host == "" || u.Port() == "" {
		return nil, fmt.Errorf("%w: smtp transport needs host and port", ErrInvalidTransport)
	}
	if tlsConfig == nil {
		tlsConfig = &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
	}
	t := &smtpTransport{addr: u.Host, host: host, from: from, tlsConfig: tlsConfig}
	if u.User != nil {
		password, _ := u.User.Password()
		t.auth = smtp.PlainAuth("", u.User.Username(), password, host)
	}
	return t, nil
}

func (t *smtpTransport) Send(ctx context.Context, msg Message) error {
	to, err := netmail.ParseAddress(msg.To)
	if err != nil {
		return fmt.Errorf("recipient: %w", err)
	}
	dialer := net.Dialer{Timeout: smtpTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", t.addr)
	if err != nil {
		return fmt.Errorf("dial smtp: %w", err)
	}
	defer conn.Close()
	if deadlineErr := conn.SetDeadline(time.Now().Add(smtpTimeout)); deadlineErr != nil {
		return fmt.Errorf("smtp deadline: %w", deadlineErr)
	}
	client, err := smtp.NewClient(conn, t.host)
	if err != nil {
		return fmt.Errorf("smtp greeting: %w", err)
	}
	if ok, _ := client.Extension("STARTTLS"); !ok {
		return ErrSMTPNoSTARTTLS
	}
	if startErr := client.StartTLS(t.tlsConfig); startErr != nil {
		return fmt.Errorf("smtp starttls: %w", startErr)
	}
	if t.auth != nil {
		if authErr := client.Auth(t.auth); authErr != nil {
			return fmt.Errorf("smtp auth: %w", authErr)
		}
	}
	if mailErr := client.Mail(t.from.Address); mailErr != nil {
		return fmt.Errorf("smtp mail from: %w", mailErr)
	}
	if rcptErr := client.Rcpt(to.Address); rcptErr != nil {
		return fmt.Errorf("smtp rcpt to: %w", rcptErr)
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	if _, writeErr := w.Write([]byte(t.render(to, msg))); writeErr != nil {
		return fmt.Errorf("smtp write: %w", writeErr)
	}
	if closeErr := w.Close(); closeErr != nil {
		return fmt.Errorf("smtp send: %w", closeErr)
	}
	if quitErr := client.Quit(); quitErr != nil {
		return fmt.Errorf("smtp quit: %w", quitErr)
	}
	return nil
}

func (t *smtpTransport) render(to *netmail.Address, msg Message) string {
	var b strings.Builder
	header := func(name, value string) {
		b.WriteString(name + ": " + value + "\r\n")
	}
	header("From", t.from.String())
	header("To", to.String())
	if msg.ReplyTo != "" {
		replyTo := netmail.Address{Address: msg.ReplyTo}
		header("Reply-To", replyTo.String())
	}
	header("Subject", mime.QEncoding.Encode("utf-8", msg.Subject))
	header("Date", time.Now().UTC().Format(time.RFC1123Z))
	header("MIME-Version", "1.0")
	header("Content-Type", "text/plain; charset=utf-8")
	b.WriteString("\r\n")
	b.WriteString(msg.Body)
	return b.String()
}
