package mail

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"fmt"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"
)

const (
	smtpUser     = "mailer"
	smtpPassword = "s3cret"
)

type smtpCapture struct {
	authLine string
	from     string
	rcpt     string
	data     string
}

type testSMTPServer struct {
	addr     string
	cert     *x509.Certificate
	starttls bool
	captured chan smtpCapture
}

func newTestSMTPServer(t *testing.T, starttls bool) *testSMTPServer {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "smtp.test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("certificate: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}},
		MinVersion:   tls.VersionTLS12,
	}
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := listener.Close(); closeErr != nil {
			t.Logf("close listener: %v", closeErr)
		}
	})
	s := &testSMTPServer{
		addr:     listener.Addr().String(),
		cert:     cert,
		starttls: starttls,
		captured: make(chan smtpCapture, 1),
	}
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			go s.serve(conn, tlsConfig)
		}
	}()
	return s
}

func (s *testSMTPServer) serve(conn net.Conn, tlsConfig *tls.Config) {
	defer conn.Close()
	var capture smtpCapture
	reader := bufio.NewReader(conn)
	reply := func(format string, args ...any) {
		fmt.Fprintf(conn, format+"\r\n", args...)
	}
	reply("220 smtp.test ready")
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		verb, arg, _ := strings.Cut(line, " ")
		switch strings.ToUpper(verb) {
		case "EHLO":
			if s.starttls {
				reply("250-smtp.test")
				reply("250 STARTTLS")
			} else {
				reply("250 smtp.test")
			}
		case "STARTTLS":
			reply("220 go ahead")
			secured := tls.Server(conn, tlsConfig)
			if handshakeErr := secured.HandshakeContext(context.Background()); handshakeErr != nil {
				return
			}
			conn = secured
			reader = bufio.NewReader(secured)
			reply = func(format string, args ...any) {
				fmt.Fprintf(secured, format+"\r\n", args...)
			}
			s.starttls = false
		case "AUTH":
			capture.authLine = arg
			raw, decodeErr := base64.StdEncoding.DecodeString(strings.TrimPrefix(arg, "PLAIN "))
			if decodeErr != nil || string(raw) != "\x00"+smtpUser+"\x00"+smtpPassword {
				reply("535 authentication failed")
				continue
			}
			reply("235 ok")
		case "MAIL":
			capture.from = arg
			reply("250 ok")
		case "RCPT":
			capture.rcpt = arg
			reply("250 ok")
		case "DATA":
			reply("354 go")
			var data strings.Builder
			for {
				dataLine, dataErr := reader.ReadString('\n')
				if dataErr != nil || dataLine == ".\r\n" {
					break
				}
				data.WriteString(dataLine)
			}
			capture.data = data.String()
			s.captured <- capture
			reply("250 queued")
		case "QUIT":
			reply("221 bye")
			return
		default:
			reply("501 unrecognized")
		}
	}
}

func newTestSMTP(t *testing.T, server *testSMTPServer, password string) Transport {
	t.Helper()
	raw := fmt.Sprintf("smtp://%s:%s@%s", smtpUser, password, server.addr)
	transport, err := ParseTransport(raw, testFrom, "")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	smtpTransport, ok := transport.(*smtpTransport)
	if !ok {
		t.Fatalf("transport is %T", transport)
	}
	pool := x509.NewCertPool()
	pool.AddCert(server.cert)
	smtpTransport.tlsConfig = &tls.Config{ServerName: "127.0.0.1", RootCAs: pool, MinVersion: tls.VersionTLS12}
	return smtpTransport
}

func TestSMTPDeliversOverSTARTTLS(t *testing.T) {
	server := newTestSMTPServer(t, true)
	transport := newTestSMTP(t, server, smtpPassword)
	event := testEvent()

	if err := transport.Send(t.Context(), NewInvitationMessage(event)); err != nil {
		t.Fatalf("send: %v", err)
	}

	got := <-server.captured
	if got.authLine == "" {
		t.Error("client never authenticated")
	}
	if got.from != "FROM:<noreply@loco.test>" || got.rcpt != "TO:<bob@acme.test>" {
		t.Errorf("envelope from = %q rcpt = %q", got.from, got.rcpt)
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
		if !strings.Contains(got.data, want) {
			t.Errorf("message lacks %q:\n%s", want, got.data)
		}
	}
}

func TestSMTPAuthFailureSurfaces(t *testing.T) {
	server := newTestSMTPServer(t, true)
	transport := newTestSMTP(t, server, "wrong")

	err := transport.Send(t.Context(), NewInvitationMessage(testEvent()))
	if err == nil || !strings.Contains(err.Error(), "smtp auth") {
		t.Fatalf("err = %v, want an auth failure", err)
	}
}

func TestSMTPRequiresSTARTTLS(t *testing.T) {
	server := newTestSMTPServer(t, false)
	transport := newTestSMTP(t, server, smtpPassword)

	err := transport.Send(t.Context(), NewInvitationMessage(testEvent()))
	if err == nil || !strings.Contains(err.Error(), ErrSMTPNoSTARTTLS.Error()) {
		t.Fatalf("err = %v, want ErrSMTPNoSTARTTLS", err)
	}
}
