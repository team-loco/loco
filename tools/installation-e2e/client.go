package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	environmentv1 "github.com/team-loco/loco/gen/go/loco/environment/v1"
	"github.com/team-loco/loco/internal/rpc"
	clisession "github.com/team-loco/loco/internal/session"

	"golang.org/x/net/html"
)

const (
	fixtureEmail    = "dev@loco.test"
	fixturePassword = "loco-dev"
	clientFile      = "/installation/installation.yaml"
	clientTimeout   = 45 * time.Minute
)

func clientAcceptance(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, clientTimeout)
	defer cancel()
	for _, operation := range []string{"validate", "plan"} {
		if _, checkErr := locoadm(ctx, operation, "--file", clientFile); checkErr != nil {
			return fmt.Errorf("initial %s: %w", operation, checkErr)
		}
	}
	if _, checkErr := kubectl(ctx, "get", "clusterissuer", "installation-ca"); checkErr != nil {
		if _, checkErr := locoadm(ctx, "apply", "--file", clientFile); checkErr == nil {
			return errors.New("initial apply unexpectedly accepted a missing issuer")
		}
		if checkErr := provisionIssuer(ctx); checkErr != nil {
			return checkErr
		}
	}
	if _, checkErr := locoadm(ctx, "apply", "--file", clientFile); checkErr != nil {
		return checkErr
	}
	if dnsErr := configureGatewayDNS(ctx, controlNamespace, false); dnsErr != nil {
		return dnsErr
	}
	if checkErr := bootstrap(ctx); checkErr != nil {
		return checkErr
	}
	if _, targetErr := locoadm(ctx, "cluster", "list"); targetErr != nil {
		return fmt.Errorf("saved bootstrap target: %w", targetErr)
	}
	if _, checkErr := locoadm(ctx, "apply", "--file", clientFile); checkErr != nil {
		return checkErr
	}
	if err := configureGatewayDNS(ctx, workerNamespace, true); err != nil {
		return err
	}
	before, err := identitySnapshot(ctx)
	if err != nil {
		return err
	}
	if _, checkErr := locoadm(ctx, "apply", "--file", clientFile); checkErr != nil {
		return checkErr
	}
	after, err := identitySnapshot(ctx)
	if err != nil {
		return err
	}
	if before != after {
		return errors.New("repeat apply replaced installation credentials or worker identity")
	}
	// A fresh local credential store must recover the existing administrator and clean up,
	// rather than requiring an IdP token or an internal installation token copied by hand.
	originalHome := os.Getenv("HOME")
	if checkErr := os.Setenv("HOME", "/tmp/operator-second"); checkErr != nil {
		return checkErr
	}
	if checkErr := bootstrap(ctx); checkErr != nil {
		return fmt.Errorf("resume from fresh operator state: %w", checkErr)
	}
	if _, checkErr := locoadm(ctx, "apply", "--file", clientFile); checkErr != nil {
		return fmt.Errorf("apply from fresh operator state: %w", checkErr)
	}
	after, err = identitySnapshot(ctx)
	if err != nil {
		return err
	}
	if before != after {
		return errors.New("fresh operator apply replaced installation credentials or worker identity")
	}
	if checkErr := os.Setenv("HOME", originalHome); checkErr != nil {
		return checkErr
	}
	if checkErr := probeNetworkPolicy(ctx); checkErr != nil {
		return checkErr
	}
	if checkErr := deployFixture(ctx); checkErr != nil {
		return checkErr
	}
	fmt.Println(
		"Installation passed: real IdP, closed bootstrap, source build, HTTPS and preserved identity.",
	)
	return nil
}

func bootstrap(ctx context.Context) error {
	cmd := exec.CommandContext(
		ctx,
		"locoadm",
		"bootstrap",
		"--file",
		clientFile,
		"--email",
		fixtureEmail,
		"--no-browser",
	)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	// Provider/CLI errors can carry credential material. Only the phase is returned.
	cmd.Stderr = io.Discard
	if checkErr := cmd.Start(); checkErr != nil {
		return fmt.Errorf("start bootstrap: %w", checkErr)
	}
	scanner := bufio.NewScanner(stdout)
	var authorization string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "http://") || strings.HasPrefix(line, "https://") {
			authorization = line
			break
		}
	}
	if authorization == "" {
		return errors.Join(errors.New("bootstrap did not start IdP authentication"), cmd.Wait())
	}
	if checkErr := completeDexLogin(ctx, authorization); checkErr != nil {
		return errors.Join(checkErr, stopProcess(cmd))
	}
	_, copyErr := io.Copy(io.Discard, stdout)
	waitErr := cmd.Wait()
	if scanner.Err() != nil || copyErr != nil || waitErr != nil {
		return errors.New("authenticated bootstrap failed")
	}
	return nil
}

func completeDexLogin(ctx context.Context, authorization string) error {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return err
	}
	client := &http.Client{Jar: jar, Timeout: time.Minute}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, authorization, nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return errors.New("open fixture IdP login failed")
	}
	defer response.Body.Close()
	document, err := html.Parse(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return errors.New("read fixture IdP form failed")
	}
	action, values, ok := loginForm(document)
	if !ok {
		return errors.New("fixture IdP did not offer its password login form")
	}
	target, err := response.Request.URL.Parse(action)
	if err != nil {
		return errors.New("invalid fixture IdP form action")
	}
	values.Set("login", fixtureEmail)
	values.Set("password", fixturePassword)
	request, err = http.NewRequestWithContext(ctx, http.MethodPost, target.String(), strings.NewReader(values.Encode()))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	result, err := client.Do(request)
	if err != nil {
		return errors.New("submit fixture IdP login failed")
	}
	defer result.Body.Close()
	if result.StatusCode != http.StatusOK {
		return errors.New("fixture IdP authentication did not complete")
	}
	return nil
}

func loginForm(node *html.Node) (string, url.Values, bool) {
	if node.Type == html.ElementNode && node.Data == "form" {
		action := ""
		for _, attribute := range node.Attr {
			if attribute.Key == "action" {
				action = attribute.Val
			}
		}
		values := url.Values{}
		var inputs func(*html.Node)
		inputs = func(n *html.Node) {
			if n.Type == html.ElementNode && n.Data == "input" {
				name, value := "", ""
				for _, attribute := range n.Attr {
					switch attribute.Key {
					case "name":
						name = attribute.Val
					case "value":
						value = attribute.Val
					}
				}
				if name != "" {
					values.Set(name, value)
				}
			}
			for child := n.FirstChild; child != nil; child = child.NextSibling {
				inputs(child)
			}
		}
		inputs(node)
		if values.Has("login") && values.Has("password") {
			return action, values, true
		}
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if action, values, ok := loginForm(child); ok {
			return action, values, true
		}
	}
	return "", nil, false
}

func identitySnapshot(ctx context.Context) (string, error) {
	data, err := kubectl(ctx, "get",
		"secrets",
		"--all-namespaces",
		"-l",
		"loco.build/installation=installation-e2e",
		"-o",
		"json",
	)
	if err != nil {
		return "", err
	}
	var result struct {
		Items []struct {
			Metadata struct {
				Name      string `json:"name"`
				Namespace string `json:"namespace"`
			} `json:"metadata"`
			Data map[string]string `json:"data"`
		} `json:"items"`
	}
	if checkErr := json.Unmarshal(data, &result); checkErr != nil {
		return "", checkErr
	}
	if len(result.Items) == 0 {
		return "", errors.New("installer created no owned credential Secrets")
	}
	// Resource versions and deployment rollout metadata may change; credential bytes may not.
	values := make(map[string]map[string]string)
	for _, item := range result.Items {
		values[item.Metadata.Namespace+"/"+item.Metadata.Name] = item.Data
	}
	agent, err := kubectl(ctx, "-n",
		workerNamespace,
		"get",
		"secret",
		"loco-worker-agent-key",
		"-o",
		"json",
	)
	if err != nil {
		return "", err
	}
	var identity struct {
		Data map[string]string `json:"data"`
	}
	if checkErr := json.Unmarshal(agent, &identity); checkErr != nil {
		return "", checkErr
	}
	if identity.Data["private-key"] == "" || identity.Data["cluster-id"] == "" {
		return "", errors.New("worker identity is incomplete")
	}
	values[workerNamespace+"/loco-worker-agent-key"] = identity.Data
	normalized, err := json.Marshal(values)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(normalized)
	return hex.EncodeToString(digest[:]), nil
}

func deployFixture(ctx context.Context) error {
	app := "/fixture/build-app"
	if _, checkErr := command(ctx, "", nil, "loco", "use", "installation-e2e/source-build"); checkErr != nil {
		organization, checkErr := command(ctx, "", nil, "loco", "org", "create", "installation-e2e")
		if checkErr != nil {
			return checkErr
		}
		organizationID := regexp.MustCompile(`[a-f0-9]{8}-[a-f0-9-]{27}`).FindString(string(organization))
		if organizationID == "" {
			return errors.New("organization creation returned no ID")
		}
		workspace, err := command(
			ctx,
			"",
			nil,
			"loco",
			"workspace",
			"create",
			"source-build",
			"--org-id",
			organizationID,
		)
		if err != nil {
			return err
		}
		workspaceID := regexp.MustCompile(`[a-f0-9]{8}-[a-f0-9-]{27}`).FindString(string(workspace))
		if workspaceID == "" {
			return errors.New("workspace creation returned no ID")
		}
		if _, checkErr := command(ctx, "", nil, "loco", "use", "installation-e2e/source-build"); checkErr != nil {
			return checkErr
		}

		api, apiErr := fixtureAPI()
		if apiErr != nil {
			return apiErr
		}
		if _, createErr := api.Environment.CreateEnvironment(
			ctx,
			&environmentv1.CreateEnvironmentRequest{
				WorkspaceId: workspaceID,
				Name:        "staging",
				Type:        environmentv1.EnvironmentType_ENVIRONMENT_TYPE_STAGING,
			},
		); createErr != nil {
			return errors.New("create staging environment failed")
		}
	}
	if checkErr := os.Setenv("LOCO_ENV", "staging"); checkErr != nil {
		return checkErr
	}
	file := `version: 1
partial: installation-smoke
services:
  build:
    dockerfile: deploy/Dockerfile
    port: 8080
    health: {path: /healthz, interval: 5, timeout: 3, failThreshold: 3, startupGracePeriod: 0}
    domains: [build.apps.installation.test]
    regions:
      us-east-1: {cpu: 100m, memory: 128Mi, replicas: {min: 1, max: 1}}
`
	if checkErr := os.WriteFile(filepath.Join(app, "loco.yaml"), []byte(file), 0600); checkErr != nil {
		return checkErr
	}
	if _, checkErr := command(ctx, app, nil, "loco", "deploy", "--yes"); checkErr != nil {
		return fmt.Errorf("source build and deploy: %w", checkErr)
	}
	if _, checkErr := os.Stat(filepath.Join(app, "loco.yaml")); checkErr != nil {
		return checkErr
	}
	// Fixture routing resolves this public hostname through the worker Gateway service.
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://build.apps.installation.test/healthz", nil)
	if err != nil {
		return err
	}
	roots, err := fixtureRoots(ctx)
	if err != nil {
		return err
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
	client := &http.Client{Timeout: time.Minute, Transport: transport}
	if err := waitHTTPS(ctx, client, request); err != nil {
		return err
	}
	config, configErr := clisession.Load()
	if configErr != nil {
		return configErr
	}
	scope, scopeErr := config.GetScope()
	if scopeErr != nil {
		return scopeErr
	}
	api, apiErr := fixtureAPI()
	if apiErr != nil {
		return apiErr
	}
	listed, listErr := api.Environment.ListEnvironments(
		ctx,
		&environmentv1.ListEnvironmentsRequest{WorkspaceId: scope.Workspace.ID},
	)
	if listErr != nil {
		return errors.New("list staging verification environment failed")
	}
	environmentID := ""
	for _, environment := range listed.GetEnvironments() {
		if environment.GetName() == "staging" {
			environmentID = environment.GetId()
		}
	}
	if environmentID == "" {
		return errors.New("staging verification environment is missing")
	}
	if _, verifyErr := locoadm(
		ctx,
		"verify",
		"--file",
		clientFile,
		"--worker",
		"staging",
		"--environment",
		environmentID,
	); verifyErr != nil {
		return fmt.Errorf("operator verification: %w", verifyErr)
	}
	return nil
}

func provisionIssuer(ctx context.Context) error {
	if _, checkErr := kubectl(ctx, "-n",
		"cert-manager",
		"rollout",
		"status",
		"deployment/cert-manager-webhook",
		"--timeout=300s",
	); checkErr != nil {
		return checkErr
	}
	documents := `apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata: {name: fixture-selfsigned}
spec: {selfSigned: {}}
---
apiVersion: cert-manager.io/v1
kind: Certificate
metadata: {name: installation-root, namespace: cert-manager}
spec:
  isCA: true
  commonName: Installation fixture root
  secretName: installation-root
  issuerRef: {name: fixture-selfsigned, kind: ClusterIssuer}
---
apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata: {name: installation-ca}
spec: {ca: {secretName: installation-root}}
`
	if _, checkErr := command(ctx, "", []byte(documents), "kubectl", "apply", "-f", "-"); checkErr != nil {
		return checkErr
	}
	if _, checkErr := kubectl(ctx, "wait",
		"--for=condition=Ready",
		"clusterissuer/installation-ca",
		"--timeout=120s",
	); checkErr != nil {
		return checkErr
	}
	return nil
}

func fixtureRoots(ctx context.Context) (*x509.CertPool, error) {
	encoded, err := kubectl(ctx, "-n",
		"cert-manager",
		"get",
		"secret",
		"installation-root",
		"-o",
		"jsonpath={.data.tls\\.crt}",
	)
	if err != nil {
		return nil, err
	}
	certificate, err := base64.StdEncoding.DecodeString(string(encoded))
	if err != nil {
		return nil, errors.New("invalid fixture CA certificate")
	}
	publicCA, createErr := os.CreateTemp("", "installation-ca-*.crt")
	if createErr != nil {
		return nil, createErr
	}
	if _, writeErr := publicCA.Write(certificate); writeErr != nil {
		return nil, errors.Join(writeErr, publicCA.Close())
	}
	if closeErr := publicCA.Close(); closeErr != nil {
		return nil, closeErr
	}
	if err := os.Setenv("SSL_CERT_FILE", publicCA.Name()); err != nil {
		return nil, err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(certificate) {
		return nil, errors.New("fixture CA certificate is not PEM")
	}
	return roots, nil
}

func probeNetworkPolicy(ctx context.Context) error {
	image := os.Getenv("E2E_PROBE_IMAGE")
	namespace := os.Getenv("E2E_BUILD_NAMESPACE")
	if image == "" || namespace == "" {
		return errors.New("network-policy fixture inputs are missing")
	}
	node, err := kubectl(ctx, "get",
		"nodes",
		"-o",
		`jsonpath={.items[0].status.addresses[?(@.type=="InternalIP")].address}`,
	)
	if err != nil {
		return err
	}
	host := strings.TrimSpace(string(node))
	for _, target := range []string{fixtureNamespace, namespace} {
		data, err := renderFixtureFile(
			"/fixture/network-probe.yaml",
			map[string]any{"Namespace": target, "Image": image},
		)
		if err != nil {
			return err
		}
		if _, applyErr := command(ctx, "", data, "kubectl", "apply", "-f", "-"); applyErr != nil {
			return applyErr
		}
		if _, checkErr := kubectl(ctx, "-n",
			target,
			"wait",
			"--for=condition=Ready",
			"pod/installation-network-probe",
			"--timeout=120s",
		); checkErr != nil {
			return checkErr
		}
		if _, checkErr := kubectl(ctx, "-n",
			target,
			"exec",
			"installation-network-probe",
			"--",
			"nc",
			"-z",
			"-w",
			"3",
			"registry.installation.localhost",
			"5000",
		); checkErr != nil {
			return fmt.Errorf("network-policy probe cannot reach registry from %s: %w", target, checkErr)
		}
	}
	if _, checkErr := kubectl(ctx, "-n",
		fixtureNamespace,
		"exec",
		"installation-network-probe",
		"--",
		"nc",
		"-z",
		"-w",
		"3",
		host,
		"6443",
	); checkErr != nil {
		return errors.New("unrestricted fixture cannot reach the Kubernetes API")
	}
	deadline := time.NewTimer(time.Minute)
	defer deadline.Stop()
	retry := time.NewTicker(time.Second)
	defer retry.Stop()
	for {
		if _, checkErr := kubectl(ctx, "-n",
			namespace,
			"exec",
			"installation-network-probe",
			"--",
			"nc",
			"-z",
			"-w",
			"3",
			host,
			"6443",
		); checkErr != nil {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return errors.New("build namespace NetworkPolicy does not block Kubernetes API access")
		case <-retry.C:
		}
	}
	for _, target := range []string{fixtureNamespace, namespace} {
		if _, checkErr := kubectl(ctx, "-n",
			target,
			"delete",
			"pod",
			"installation-network-probe",
			"--wait=false",
		); checkErr != nil {
			return checkErr
		}
	}
	return nil
}

func kubectl(ctx context.Context, args ...string) ([]byte, error) {
	return command(ctx, "", nil, "kubectl", args...)
}
func locoadm(ctx context.Context, args ...string) ([]byte, error) {
	return command(ctx, "", nil, "locoadm", args...)
}

// The file-store shape is owned by cmd/internal/keychain. The harness reads the
// normal CLI-issued session to call application APIs that have no CLI subcommand.
func fixtureAPI() (*rpc.Clients, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(home, ".loco", "credentials.json"))
	if err != nil {
		return nil, err
	}
	var stored struct {
		Host  string `json:"host"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal(data, &stored); err != nil {
		return nil, err
	}
	return rpc.ForHost(stored.Host, stored.Token), nil
}

func waitHTTPS(ctx context.Context, client *http.Client, request *http.Request) error {
	deadline, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		response, err := client.Do(request.Clone(deadline))
		if err == nil {
			_, readErr := io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
			closeErr := response.Body.Close()
			if response.StatusCode == http.StatusOK && readErr == nil && closeErr == nil {
				return nil
			}
		}
		select {
		case <-deadline.Done():
			return errors.New("application HTTPS readiness did not succeed")
		case <-ticker.C:
		}
	}
}
