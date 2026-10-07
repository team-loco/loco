package notify

import (
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/mail"
	"net/textproto"
	"strconv"
	"strings"
	"testing"
)

type receivedMail struct {
	from string
	to   []string
	data string
}

const testRecipient = "a@b.test"

func fakeSMTP(t *testing.T) (string, int, <-chan receivedMail) {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := ln.Close(); closeErr != nil {
			t.Logf("close listener: %v", closeErr)
		}
	})
	out := make(chan receivedMail, 1)
	go func() {
		conn, acceptErr := ln.Accept()
		if acceptErr != nil {
			return
		}
		defer func() {
			if closeErr := conn.Close(); closeErr != nil {
				t.Logf("close conn: %v", closeErr)
			}
		}()
		tp := textproto.NewConn(conn)
		var got receivedMail
		reply := func(line string) bool {
			return tp.PrintfLine("%s", line) == nil
		}
		if !reply("220 fake ESMTP") {
			return
		}
		for {
			line, readErr := tp.ReadLine()
			if readErr != nil {
				return
			}
			cmd := strings.ToUpper(line)
			switch {
			case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
				reply("250 fake")
			case strings.HasPrefix(cmd, "MAIL FROM:"):
				got.from = line[len("MAIL FROM:"):]
				reply("250 ok")
			case strings.HasPrefix(cmd, "RCPT TO:"):
				got.to = append(got.to, line[len("RCPT TO:"):])
				reply("250 ok")
			case cmd == "DATA":
				reply("354 go ahead")
				body, dataErr := io.ReadAll(tp.DotReader())
				if dataErr != nil {
					return
				}
				got.data = string(body)
				reply("250 queued")
			case cmd == "QUIT":
				reply("221 bye")
				out <- got
				return
			default:
				reply("502 unsupported")
			}
		}
	}()
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("unexpected listener address %T", ln.Addr())
	}
	return addr.IP.String(), addr.Port, out
}

func TestSMTPMailerSendsMultipartMessage(t *testing.T) {
	host, port, received := fakeSMTP(t)
	cfg, err := ParseSMTPConfig(host, strconv.Itoa(port), "", "", "Loco <noreply@loco.test>", "none")
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	email := Email{
		To:          "dev@acme.test",
		Subject:     "Confirm your email — café",
		Heading:     "Confirm <your> email",
		Paragraphs:  []string{"Hello & welcome."},
		ActionLabel: "Confirm",
		ActionURL:   "https://app.loco.test/auth/confirm?token_hash=abc&type=signup",
	}
	msg, err := email.Render()
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if sendErr := NewSMTPMailer(cfg).Send(t.Context(), msg); sendErr != nil {
		t.Fatalf("send: %v", sendErr)
	}

	got := <-received
	if got.from != "<noreply@loco.test>" || len(got.to) != 1 || got.to[0] != "<dev@acme.test>" {
		t.Fatalf("envelope = %q %q", got.from, got.to)
	}
	parsed, err := mail.ReadMessage(strings.NewReader(got.data))
	if err != nil {
		t.Fatalf("parse message: %v", err)
	}
	subject, err := new(mime.WordDecoder).DecodeHeader(parsed.Header.Get("Subject"))
	if err != nil || subject != email.Subject {
		t.Fatalf("subject = %q (%v)", subject, err)
	}
	mediaType, params, err := mime.ParseMediaType(parsed.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/alternative" {
		t.Fatalf("content type = %q (%v)", mediaType, err)
	}
	reader := multipart.NewReader(parsed.Body, params["boundary"])
	parts := map[string]string{}
	for {
		part, partErr := reader.NextPart()
		if partErr == io.EOF {
			break
		}
		if partErr != nil {
			t.Fatalf("next part: %v", partErr)
		}
		body, readErr := io.ReadAll(part)
		if readErr != nil {
			t.Fatalf("read part: %v", readErr)
		}
		ct, _, ctErr := mime.ParseMediaType(part.Header.Get("Content-Type"))
		if ctErr != nil {
			t.Fatalf("part content type: %v", ctErr)
		}
		parts[ct] = string(body)
	}
	if !strings.Contains(parts["text/plain"], "Confirm: "+email.ActionURL) {
		t.Fatalf("text part = %q", parts["text/plain"])
	}
	html := parts["text/html"]
	if !strings.Contains(html, "Confirm &lt;your&gt; email") || !strings.Contains(html, "Hello &amp; welcome.") {
		t.Fatalf("html part not escaped: %q", html)
	}
	if !strings.Contains(html, `href="https://app.loco.test/auth/confirm?token_hash=abc&amp;type=signup"`) {
		t.Fatalf("html link missing: %q", html)
	}
}

func TestRenderDropsUnsafeLinks(t *testing.T) {
	msg, err := Email{
		To:          testRecipient,
		Subject:     "s",
		Heading:     "h",
		ActionLabel: "Go",
		ActionURL:   "javascript:alert(1)",
	}.Render()
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if strings.Contains(msg.HTML, `href="javascript:`) {
		t.Fatalf("unsafe href rendered: %q", msg.HTML)
	}
}

func TestParseSMTPConfig(t *testing.T) {
	cfg, err := ParseSMTPConfig("smtp.example.test", "", "u", "p", "noreply@example.test", "")
	if err != nil {
		t.Fatalf("defaults: %v", err)
	}
	if cfg.Port != 587 || cfg.TLS != TLSStartTLS {
		t.Fatalf("defaults = %+v", cfg)
	}
	implicit, err := ParseSMTPConfig("smtp.example.test", "465", "", "", "noreply@example.test", "")
	if err != nil || implicit.TLS != TLSImplicit {
		t.Fatalf("port 465 = %+v (%v)", implicit, err)
	}
	for name, args := range map[string][6]string{
		"no host":    {"", "", "", "", testRecipient, ""},
		"no from":    {"h", "", "", "", "", ""},
		"bad from":   {"h", "", "", "", "not an address", ""},
		"bad port":   {"h", "seventy", "", "", testRecipient, ""},
		"bad tls":    {"h", "25", "", "", testRecipient, "maybe"},
		"port range": {"h", "70000", "", "", testRecipient, ""},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseSMTPConfig(args[0], args[1], args[2], args[3], args[4], args[5]); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}
