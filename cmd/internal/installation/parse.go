package installation

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/mail"
	"net/url"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v2"
)

const (
	maxDNSLabelLength = 63
	maxDNSNameLength  = 253
	maxPort           = 65535
)

var (
	errDocument = errors.New("installation must contain one YAML object with known fields and no duplicate keys")
	errRead     = errors.New("cannot read installation file")
	errRequired = errors.New("required field is missing")
	errInvalid  = errors.New("invalid or unsupported setting")
	secretKey   = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
	dnsLabel    = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)
)

// Load reads installation intent only; it does not contact clusters or read secrets.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, errRead
	}
	return Parse(data)
}

// Parse returns safe errors without retaining decoder errors or source values.
func Parse(data []byte) (*Config, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.SetStrict(true)
	var cfg Config
	if err := decoder.Decode(&cfg); err != nil {
		return nil, errDocument
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errDocument
	}
	var raw any
	if err := yaml.UnmarshalStrict(data, &raw); err != nil {
		return nil, errDocument
	}
	if err := shape(raw, reflect.TypeOf(cfg), ""); err != nil {
		return nil, err
	}
	if err := Validate(&cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func Validate(cfg *Config) error {
	if cfg == nil {
		return errDocument
	}
	if err := required(reflect.ValueOf(*cfg), ""); err != nil {
		return err
	}
	if cfg.Version != "loco.build/admin/v1alpha1" {
		return field("version")
	}
	if strings.Contains(cfg.Release.Manifest, "://") {
		return field("release.manifest")
	}
	if !label(cfg.Name) {
		return field("name")
	}
	cp := cfg.ControlPlane
	if cp.Auth.BootstrapClientID != cp.Auth.Audience {
		return field("controlPlane.auth.bootstrapClientId")
	}
	if err := target(cp.Kubernetes, "controlPlane.kubernetes"); err != nil {
		return err
	}
	for _, endpoint := range []struct{ path, value string }{
		{"controlPlane.apiURL", cp.APIURL},
		{"controlPlane.webURL", cp.WebURL},
		{"controlPlane.auth.issuer", cp.Auth.Issuer},
		{"controlPlane.sourceBucket.endpoint", cp.SourceBucket.Endpoint},
		{"controlPlane.openbao.snapshots.endpoint", cp.OpenBao.Snapshots.Endpoint},
		{"controlPlane.observability.ingestURL", cp.Observability.IngestURL},
	} {
		if !safeURL(endpoint.value) {
			return field(endpoint.path)
		}
	}
	if !bootstrapRedirect(cp.Auth.BootstrapRedirectURI) {
		return field("controlPlane.auth.bootstrapRedirectURI")
	}
	if cp.Auth.SignupMode != "open" && cp.Auth.SignupMode != "closed" {
		return field("controlPlane.auth.signupMode")
	}
	if !cp.OpenBao.Managed {
		return field("controlPlane.openbao.managed")
	}
	if cp.OpenBao.Seal.Type != "static" {
		return field("controlPlane.openbao.seal.type")
	}
	if !cp.Observability.Enabled {
		return field("controlPlane.observability.enabled")
	}
	if !registryEndpoint(cp.Registry.Endpoint) {
		return field("controlPlane.registry.endpoint")
	}
	if _, err := mail.ParseAddress(cp.Email.From); err != nil {
		return field("controlPlane.email.from")
	}
	if err := certificate(cp.Certificates, "controlPlane.certificates"); err != nil {
		return err
	}
	names := make(map[string]bool)
	for i, w := range cfg.Workers {
		path := fmt.Sprintf("workers[%d]", i)
		if !label(w.Name) || names[w.Name] {
			return field(path + ".name")
		}
		names[w.Name] = true
		if err := target(w.Kubernetes, path+".kubernetes"); err != nil {
			return err
		}
		if w.Tier != "staging" && w.Tier != "production" {
			return field(path + ".tier")
		}
		if !domain(w.AppsDomain) {
			return field(path + ".appsDomain")
		}
		if err := certificate(w.Certificates, path+".certificates"); err != nil {
			return err
		}
	}
	return nil
}

func shape(raw any, typ reflect.Type, path string) error {
	switch typ.Kind() {
	case reflect.Struct:
		object, ok := raw.(map[any]any)
		if !ok {
			return field(path)
		}
		for definition := range typ.Fields() {
			name := definition.Tag.Get("yaml")
			next := name
			if path != "" {
				next = path + "." + name
			}
			value, exists := object[name]
			if !exists {
				return fmt.Errorf("%s: %w", next, errRequired)
			}
			if err := shape(value, definition.Type, next); err != nil {
				return err
			}
		}
	case reflect.Slice:
		values, ok := raw.([]any)
		if !ok {
			return field(path)
		}
		for i, value := range values {
			if err := shape(value, typ.Elem(), fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	case reflect.String:
		if _, ok := raw.(string); !ok {
			return field(path)
		}
	case reflect.Bool:
		if _, ok := raw.(bool); !ok {
			return field(path)
		}
	}
	return nil
}

func required(value reflect.Value, path string) error {
	switch value.Kind() {
	case reflect.Struct:
		for i := range value.NumField() {
			name := value.Type().Field(i).Tag.Get("yaml")
			next := name
			if path != "" {
				next = path + "." + name
			}
			if err := required(value.Field(i), next); err != nil {
				return err
			}
		}
	case reflect.String:
		if strings.TrimSpace(value.String()) == "" {
			return fmt.Errorf("%s: %w", path, errRequired)
		}
		if strings.HasSuffix(path, ".key") && strings.Contains(path, "Ref.") &&
			(len(value.String()) > maxDNSNameLength || !secretKey.MatchString(value.String())) {
			return field(path)
		}
		if strings.HasSuffix(path, ".name") && strings.Contains(path, "Ref.") && !dnsName(value.String()) {
			return field(path)
		}
	case reflect.Slice:
		if value.IsNil() {
			return fmt.Errorf("%s: %w", path, errRequired)
		}
		for i := range value.Len() {
			if err := required(value.Index(i), fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	}
	return nil
}

func field(path string) error { return fmt.Errorf("%s: %w", path, errInvalid) }
func label(value string) bool { return len(value) <= maxDNSLabelLength && dnsLabel.MatchString(value) }
func dnsName(value string) bool {
	if len(value) > maxDNSNameLength {
		return false
	}
	for part := range strings.SplitSeq(value, ".") {
		if !label(part) {
			return false
		}
	}
	return true
}
func domain(value string) bool {
	return strings.Contains(value, ".") && net.ParseIP(value) == nil && dnsName(value)
}
func registryEndpoint(value string) bool {
	if !safeURL(value) {
		return false
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Path != "" && parsed.Path != "/") || parsed.RawPath != "" {
		return false
	}
	if !dnsName(parsed.Hostname()) && net.ParseIP(parsed.Hostname()) == nil {
		return false
	}
	if parsed.Port() != "" {
		port, err := strconv.Atoi(parsed.Port())
		return err == nil && port > 0 && port <= maxPort
	}
	return !strings.HasSuffix(parsed.Host, ":")
}
func safeURL(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && (parsed.Scheme == "https" || parsed.Scheme == "http") && parsed.Hostname() != "" &&
		parsed.User == nil &&
		parsed.RawQuery == "" &&
		!parsed.ForceQuery &&
		parsed.Fragment == ""
}
func target(value KubernetesTarget, path string) error {
	if !label(value.Namespace) {
		return field(path + ".namespace")
	}
	return nil
}
func certificate(value Certificates, path string) error {
	if value.IssuerRef.Kind != "Issuer" && value.IssuerRef.Kind != "ClusterIssuer" {
		return field(path + ".issuerRef.kind")
	}
	return nil
}

func bootstrapRedirect(value string) bool {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "http" || parsed.Hostname() != "127.0.0.1" || parsed.Path != "/callback" ||
		parsed.RawPath != "" ||
		parsed.User != nil ||
		parsed.RawQuery != "" ||
		parsed.ForceQuery ||
		parsed.Fragment != "" {
		return false
	}
	port, err := strconv.Atoi(parsed.Port())
	return err == nil && port > 0 && port <= maxPort
}
