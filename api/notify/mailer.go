package notify

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"strconv"
	"strings"
	"time"
)

type Message struct {
	To      string
	Subject string
	Text    string
	HTML    string
}

type Mailer interface {
	Send(ctx context.Context, msg Message) error
}

type TLSMode string

const (
	TLSStartTLS TLSMode = "starttls"
	TLSImplicit TLSMode = "tls"
	TLSNone     TLSMode = "none"
)

type SMTPConfig struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
	TLS      TLSMode
}

func ParseSMTPConfig(host, port, username, password, from, tlsMode string) (SMTPConfig, error) {
	cfg := SMTPConfig{
		Host:     strings.TrimSpace(host),
		Username: username,
		Password: password,
		From:     strings.TrimSpace(from),
		TLS:      TLSMode(strings.TrimSpace(tlsMode)),
	}
	if cfg.Host == "" {
		return SMTPConfig{}, errors.New("SMTP host is required")
	}
	if cfg.From == "" {
		return SMTPConfig{}, errors.New("SMTP from address is required")
	}
	if _, err := mail.ParseAddress(cfg.From); err != nil {
		return SMTPConfig{}, fmt.Errorf("SMTP from address %q: %w", cfg.From, err)
	}
	cfg.Port = 587
	if port != "" {
		p, err := strconv.Atoi(port)
		if err != nil || p <= 0 || p > 65535 {
			return SMTPConfig{}, fmt.Errorf("SMTP port %q is not a port number", port)
		}
		cfg.Port = p
	}
	if cfg.TLS == "" {
		cfg.TLS = TLSStartTLS
		if cfg.Port == 465 {
			cfg.TLS = TLSImplicit
		}
	}
	switch cfg.TLS {
	case TLSStartTLS:
		return cfg, nil
	case TLSImplicit:
		return cfg, nil
	case TLSNone:
		return cfg, nil
	default:
		return SMTPConfig{}, fmt.Errorf("unknown SMTP TLS mode %q", cfg.TLS)
	}
}

type SMTPMailer struct {
	cfg     SMTPConfig
	timeout time.Duration
}

func NewSMTPMailer(cfg SMTPConfig) *SMTPMailer {
	return &SMTPMailer{cfg: cfg, timeout: 30 * time.Second}
}

func (m *SMTPMailer) Send(ctx context.Context, msg Message) error {
	to, err := mail.ParseAddress(msg.To)
	if err != nil {
		return fmt.Errorf("recipient %q: %w", msg.To, err)
	}
	from, err := mail.ParseAddress(m.cfg.From)
	if err != nil {
		return fmt.Errorf("sender %q: %w", m.cfg.From, err)
	}
	body, err := buildMessage(from, to, msg)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, m.timeout)
	defer cancel()
	addr := net.JoinHostPort(m.cfg.Host, strconv.Itoa(m.cfg.Port))
	conn, err := m.dial(ctx, addr)
	if err != nil {
		return fmt.Errorf("connect to %s: %w", addr, err)
	}
	if deadline, ok := ctx.Deadline(); ok {
		if deadlineErr := conn.SetDeadline(deadline); deadlineErr != nil {
			return fmt.Errorf("set deadline: %w", deadlineErr)
		}
	}

	client, err := smtp.NewClient(conn, m.cfg.Host)
	if err != nil {
		return fmt.Errorf("smtp handshake: %w", err)
	}
	defer func() {
		if closeErr := client.Close(); closeErr != nil && !errors.Is(closeErr, net.ErrClosed) {
			slog.DebugContext(ctx, "smtp close", "error", closeErr)
		}
	}()

	if m.cfg.TLS == TLSStartTLS {
		if tlsErr := client.StartTLS(&tls.Config{ServerName: m.cfg.Host, MinVersion: tls.VersionTLS12}); tlsErr != nil {
			return fmt.Errorf("starttls: %w", tlsErr)
		}
	}
	if m.cfg.Username != "" {
		if authErr := client.Auth(smtp.PlainAuth("", m.cfg.Username, m.cfg.Password, m.cfg.Host)); authErr != nil {
			return fmt.Errorf("smtp auth: %w", authErr)
		}
	}
	if mailErr := client.Mail(from.Address); mailErr != nil {
		return fmt.Errorf("smtp mail from: %w", mailErr)
	}
	if rcptErr := client.Rcpt(to.Address); rcptErr != nil {
		return fmt.Errorf("smtp rcpt to: %w", rcptErr)
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	if _, writeErr := w.Write(body); writeErr != nil {
		return fmt.Errorf("smtp write: %w", writeErr)
	}
	if closeErr := w.Close(); closeErr != nil {
		return fmt.Errorf("smtp end data: %w", closeErr)
	}
	return client.Quit()
}

func (m *SMTPMailer) dial(ctx context.Context, addr string) (net.Conn, error) {
	if m.cfg.TLS == TLSImplicit {
		d := &tls.Dialer{Config: &tls.Config{ServerName: m.cfg.Host, MinVersion: tls.VersionTLS12}}
		return d.DialContext(ctx, "tcp", addr)
	}
	var d net.Dialer
	return d.DialContext(ctx, "tcp", addr)
}

func buildMessage(from, to *mail.Address, msg Message) ([]byte, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	headers := []string{
		"From: " + from.String(),
		"To: " + to.String(),
		"Subject: " + mime.QEncoding.Encode("utf-8", msg.Subject),
		"Date: " + time.Now().UTC().Format(time.RFC1123Z),
		"Message-ID: " + messageID(from.Address),
		"MIME-Version: 1.0",
		"Content-Type: multipart/alternative; boundary=" + mw.Boundary(),
	}
	var out bytes.Buffer
	out.WriteString(strings.Join(headers, "\r\n"))
	out.WriteString("\r\n\r\n")

	for _, part := range []struct {
		contentType string
		body        string
	}{
		{"text/plain; charset=utf-8", msg.Text},
		{"text/html; charset=utf-8", msg.HTML},
	} {
		if part.body == "" {
			continue
		}
		pw, err := mw.CreatePart(textproto.MIMEHeader{
			"Content-Type":              {part.contentType},
			"Content-Transfer-Encoding": {"quoted-printable"},
		})
		if err != nil {
			return nil, fmt.Errorf("mime part: %w", err)
		}
		qp := quotedprintable.NewWriter(pw)
		if _, err := qp.Write([]byte(part.body)); err != nil {
			return nil, fmt.Errorf("encode part: %w", err)
		}
		if err := qp.Close(); err != nil {
			return nil, fmt.Errorf("encode part: %w", err)
		}
	}
	if err := mw.Close(); err != nil {
		return nil, fmt.Errorf("close mime: %w", err)
	}
	out.Write(buf.Bytes())
	return out.Bytes(), nil
}

func messageID(from string) string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("<%d@loco>", time.Now().UnixNano())
	}
	domain := "loco"
	if _, after, found := strings.CutLast(from, "@"); found {
		domain = after
	}
	return "<" + hex.EncodeToString(b) + "@" + domain + ">"
}

type LogMailer struct{}

func (LogMailer) Send(ctx context.Context, msg Message) error {
	slog.WarnContext(ctx, "email dropped: no SMTP server configured", "to", msg.To, "subject", msg.Subject)
	return nil
}
