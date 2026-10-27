package mailtest

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

const capturedBuffer = 16

const (
	User     = "mailer"
	Password = "s3cret"
)

type Capture struct {
	AuthLine string
	From     string
	Rcpt     string
	Data     string
}

type SMTPServer struct {
	Addr     string
	Cert     *x509.Certificate
	starttls bool
	Captured chan Capture
}

func NewSMTPServer(t *testing.T, starttls bool) *SMTPServer {
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
	s := &SMTPServer{
		Addr:     listener.Addr().String(),
		Cert:     cert,
		starttls: starttls,
		Captured: make(chan Capture, capturedBuffer),
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

func (s *SMTPServer) serve(conn net.Conn, tlsConfig *tls.Config) {
	defer conn.Close()
	var capture Capture
	offerTLS := s.starttls
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
			if offerTLS {
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
			offerTLS = false
		case "AUTH":
			capture.AuthLine = arg
			raw, decodeErr := base64.StdEncoding.DecodeString(strings.TrimPrefix(arg, "PLAIN "))
			if decodeErr != nil || string(raw) != "\x00"+User+"\x00"+Password {
				reply("535 authentication failed")
				continue
			}
			reply("235 ok")
		case "MAIL":
			capture.From = arg
			reply("250 ok")
		case "RCPT":
			capture.Rcpt = arg
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
			capture.Data = data.String()
			s.Captured <- capture
			reply("250 queued")
		case "QUIT":
			reply("221 bye")
			return
		default:
			reply("501 unrecognized")
		}
	}
}

func (s *SMTPServer) URL(password string) string {
	return fmt.Sprintf("smtp://%s:%s@%s", User, password, s.Addr)
}

func (s *SMTPServer) ClientTLS() *tls.Config {
	pool := x509.NewCertPool()
	pool.AddCert(s.Cert)
	return &tls.Config{ServerName: "127.0.0.1", RootCAs: pool, MinVersion: tls.VersionTLS12}
}
