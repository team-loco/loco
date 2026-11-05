package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

var (
	errAuditAccept              = errors.New("argument audit accept failed")
	errAuditAcknowledgement     = errors.New("argument audit acknowledgement failed")
	errAuditCredentialURL       = errors.New("secret audit: invalid credential URL")
	errAuditDeadline            = errors.New("argument audit deadline failed")
	errAuditExecutable          = errors.New("unknown fixture executable")
	errAuditRegistryAuth        = errors.New("secret audit: invalid registry auth")
	errAuditRegistryCredentials = errors.New("secret audit: invalid registry credentials")
	errAuditStart               = errors.New("start argument audit failed")
	errAuditUnavailable         = errors.New("argument audit unavailable")
	errAuditWrite               = errors.New("argument audit write failed")
	errFileAudit                = errors.New("operator file audit failed")
	errFileAuditLimit           = errors.New("operator file audit limit exceeded")
	errFileAuditOpen            = errors.New("operator file audit open failed")
	errFileAuditPath            = errors.New("operator file audit path failed")
	errFileAuditRead            = errors.New("operator file audit read failed")
	errFileAuditStat            = errors.New("operator file audit stat failed")
	errFixtureExecutable        = errors.New("fixture executable failed")
	errHelmAuditCompression     = errors.New("helm audit compression invalid")
	errHelmAuditDecode          = errors.New("helm audit decoding failed")
	errHelmAuditRelease         = errors.New("helm audit release exceeds limit or is invalid")
	errInspection               = errors.New("secret inspection failed")
	errInspectionCAInvalid      = errors.New("secret inspection CA invalid")
	errInspectionCAUnavailable  = errors.New("secret inspection CA unavailable")
	errInspectionDecode         = errors.New("secret inspection decoding failed")
	errInspectionIdentity       = errors.New("secret inspection identity unavailable")
	errInspectionRejected       = errors.New("secret inspection rejected")
	errInspectionRequest        = errors.New("secret inspection request failed")
)

const auditLimit = 32 << 20
const auditSocket = "/tmp/installation-audit.sock"
const helmExecutable = "helm"
const kubectlExecutable = "kubectl"

type auditKey struct{}
type leakAudit struct {
	mu              sync.Mutex
	captures        map[string][]byte
	canaries        [][]byte
	exceeded        bool
	argumentFailure bool
	canaryBytes     int
}

func newLeakAudit() *leakAudit { return &leakAudit{captures: make(map[string][]byte)} }
func (a *leakAudit) capture(category string, data []byte) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if (category == "Helm history" || category == "operator files") && bytes.Contains(a.captures[category], data) {
		return
	}
	size := len(data)
	for _, previous := range a.captures {
		size += len(previous)
	}
	if size > auditLimit {
		a.exceeded = true
		return
	}
	a.captures[category] = append(a.captures[category], data...)
}
func (a *leakAudit) add(value []byte) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(value) < 8 {
		return
	}
	for _, previous := range a.canaries {
		if bytes.Equal(previous, value) {
			return
		}
	}
	if a.canaryBytes+len(value)*5 > auditLimit {
		a.exceeded = true
		return
	}
	a.canaryBytes += len(value) * 5
	a.canaries = append(a.canaries,
		bytes.Clone(value),
		[]byte(base64.StdEncoding.EncodeToString(value)),
		[]byte(hex.EncodeToString(value)),
	)
}
func (a *leakAudit) check(phase string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.argumentFailure {
		return fmt.Errorf("secret audit %s: subprocess argument capture failed", phase)
	}
	if a.exceeded {
		return fmt.Errorf("secret audit %s: capture limit exceeded", phase)
	}
	for category, data := range a.captures {
		for _, value := range a.canaries {
			if bytes.Contains(data, value) {
				return fmt.Errorf("secret audit %s: %s leak", phase, category)
			}
		}
	}
	return nil
}

type auditWriter struct {
	audit    *leakAudit
	category string
}

func (w auditWriter) Write(p []byte) (int, error) { w.audit.capture(w.category, p); return len(p), nil }

// The wrappers acknowledge delivery before exec, so even short-lived child argv is observed.
func auditExecutable() error {
	name := filepath.Base(os.Args[0])
	var executable string
	switch name {
	case helmExecutable:
		executable = "/usr/local/libexec/helm"
	case kubectlExecutable:
		executable = "/usr/local/libexec/kubectl"
	default:
		return errAuditExecutable
	}
	connection, err := (&net.Dialer{Timeout: time.Second}).DialContext(context.Background(), "unix", auditSocket)
	if err != nil {
		return errAuditUnavailable
	}
	defer connection.Close()
	if err = connection.SetDeadline(time.Now().Add(time.Second)); err != nil {
		return errAuditDeadline
	}
	if err = json.NewEncoder(connection).Encode(os.Args); err != nil {
		return errAuditWrite
	}
	var ack [1]byte
	if _, err = io.ReadFull(connection, ack[:]); err != nil {
		return errAuditAcknowledgement
	}
	// Paths are fixed in the fixture image; no shell or PATH lookup is involved.
	//nolint:gosec // Fixed executable selected above; argv is forwarded without a shell.
	if err = syscall.Exec(executable, os.Args, os.Environ()); err != nil {
		return errFixtureExecutable
	}
	return nil
}
func (a *leakAudit) listen(path string) (func() error, error) {
	listener, err := (&net.ListenConfig{}).Listen(context.Background(), "unix", path)
	if err != nil {
		return nil, errAuditStart
	}
	done := make(chan error, 1)
	go func() {
		for {
			connection, acceptErr := listener.Accept()
			if errors.Is(acceptErr, net.ErrClosed) {
				done <- nil
				return
			}
			if acceptErr != nil {
				done <- errAuditAccept
				return
			}
			if captureErr := a.captureArguments(connection); captureErr != nil {
				a.mu.Lock()
				a.argumentFailure = true
				a.mu.Unlock()
			}
		}
	}()
	return func() error { closeErr := listener.Close(); return errors.Join(closeErr, <-done) }, nil
}

func (a *leakAudit) captureArguments(connection net.Conn) (result error) {
	defer func() { result = errors.Join(result, connection.Close()) }()
	if err := connection.SetDeadline(time.Now().Add(time.Second)); err != nil {
		return err
	}
	var args []string
	if err := json.NewDecoder(io.LimitReader(connection, auditLimit+1)).Decode(&args); err != nil {
		return err
	}
	a.capture("subprocess arguments", []byte(strings.Join(args, "\x00")))
	_, err := connection.Write([]byte{1})
	return err
}

type auditSecret struct {
	Metadata struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	} `json:"metadata"`
	Type string            `json:"type"`
	Data map[string][]byte `json:"data"`
}

func inspectionSecrets(ctx context.Context) ([]auditSecret, error) {
	ca, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/ca.crt")
	if err != nil {
		return nil, errInspectionCAUnavailable
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		return nil, errInspectionCAInvalid
	}
	token, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/token")
	if err != nil {
		return nil, errInspectionIdentity
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: time.Minute}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		"https://kubernetes.default.svc/api/v1/secrets",
		nil,
	)
	if err != nil {
		return nil, errInspectionRequest
	}
	request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
	response, err := client.Do(request)
	if err != nil {
		return nil, errInspection
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, errInspectionRejected
	}
	var result struct {
		Items []auditSecret `json:"items"`
	}
	if err = json.NewDecoder(io.LimitReader(response.Body, auditLimit)).Decode(&result); err != nil {
		return nil, errInspectionDecode
	}
	return result.Items, nil
}
func (a *leakAudit) inspect(ctx context.Context, phase string) error {
	secrets, err := inspectionSecrets(ctx)
	if err != nil {
		return err
	}
	if err = a.collectSecrets(secrets); err != nil {
		return err
	}
	if err = a.inspectFiles("/tmp"); err != nil {
		return err
	}

	return a.check(phase)
}
func decodeRelease(encoded []byte) ([]byte, error) {
	compressed, err := base64.StdEncoding.DecodeString(string(encoded))
	if err != nil {
		return nil, errHelmAuditDecode
	}
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return nil, errHelmAuditCompression
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, auditLimit+1))
	if err != nil || len(data) > auditLimit {
		return nil, errHelmAuditRelease
	}
	return data, nil
}

func (a *leakAudit) inspectFiles(path string) error {
	root, err := os.OpenRoot(path)
	if err != nil {
		return errFileAuditOpen
	}
	defer root.Close()
	return filepath.WalkDir(path, func(file string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return errFileAudit
		}
		if entry.IsDir() {
			return nil
		}
		info, statErr := entry.Info()
		if statErr != nil {
			return errFileAuditStat
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		relative, relErr := filepath.Rel(path, file)
		if relErr != nil {
			return errFileAuditPath
		}
		// Include every HOME artifact, and private temporary files left by the CLI/Helm.
		if !strings.HasPrefix(relative, "operator-first/") && !strings.HasPrefix(relative, "operator-second/") &&
			info.Mode().Perm()&0077 != 0 {
			return nil
		}
		if info.Size() > auditLimit {
			return errFileAuditLimit
		}
		opened, openErr := root.Open(relative)
		if openErr != nil {
			return errFileAuditOpen
		}
		data, readErr := io.ReadAll(io.LimitReader(opened, auditLimit+1))
		closeErr := opened.Close()
		if readErr != nil || closeErr != nil || len(data) > auditLimit {
			return errFileAuditRead
		}
		a.capture("operator files", data)
		return nil
	})
}

func (a *leakAudit) collectSecrets(secrets []auditSecret) error {
	for _, secret := range secrets {
		if !strings.HasPrefix(secret.Metadata.Namespace, "loco-") || secret.Type == "helm.sh/release.v1" {
			continue
		}
		for key, value := range secret.Data {
			if !sensitiveSecretField(key) {
				continue
			}
			a.add(value)
			if key == "email-transport" || key == "database-url" || key == "DATABASE_URL" || key == "url" {
				endpoint, parseErr := url.Parse(string(value))
				if parseErr != nil {
					return errAuditCredentialURL
				}
				if endpoint.User != nil {
					password, _ := endpoint.User.Password()
					a.add([]byte(password))
				}
			}
			// Recovery shares and PEM bodies are independently usable secret material.
			if key == "recovery-keys" {
				for line := range bytes.SplitSeq(value, []byte("\n")) {
					a.add(line)
				}
			}
			if key == "tls.key" {
				if block, _ := pem.Decode(value); block != nil {
					a.add(block.Bytes)
				}
			}
			if key == ".dockerconfigjson" {
				var config struct {
					Auths map[string]struct {
						Password string `json:"password"`
						Auth     string `json:"auth"`
					} `json:"auths"`
				}
				if json.Unmarshal(value, &config) != nil {
					return errAuditRegistryCredentials
				}
				for _, auth := range config.Auths {
					a.add([]byte(auth.Password))
					a.add([]byte(auth.Auth))
					decoded, decodeErr := base64.StdEncoding.DecodeString(auth.Auth)
					if decodeErr != nil {
						return errAuditRegistryAuth
					}
					if _, password, ok := bytes.Cut(decoded, []byte(":")); ok {
						a.add(password)
					}
				}
			}
		}
	}
	for _, secret := range secrets {
		if secret.Type != "helm.sh/release.v1" {
			continue
		}
		data, decodeErr := decodeRelease(secret.Data["release"])
		if decodeErr != nil {
			return decodeErr
		}
		a.capture("Helm history", data)
	}
	return nil
}

type boundedOutput struct {
	bytes.Buffer
	exceeded bool
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	size := len(p)
	available := auditLimit - b.Len()
	if size > available {
		b.exceeded = true
		p = p[:available]
	}
	_, err := b.Buffer.Write(p)
	return size, err
}

func sensitiveSecretField(key string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(key, "_", "-"))
	if strings.Contains(normalized, "public") || strings.Contains(normalized, "fingerprint") ||
		strings.Contains(normalized, "cluster-id") ||
		strings.HasSuffix(normalized, ".crt") {
		return false
	}
	// Loco's generated Secrets also contain public environment URLs and identifiers.
	for _, part := range []string{"password", "token", "secret", "key"} {
		if strings.Contains(normalized, part) {
			return true
		}
	}
	return normalized == "url" || normalized == "database-url" || normalized == ".dockerconfigjson" ||
		normalized == "email-transport"
}
