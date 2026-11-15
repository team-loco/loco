package main

import (
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
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"time"

	environmentv1 "github.com/team-loco/loco/gen/go/loco/environment/v1"
	"github.com/team-loco/loco/internal/rpc"
	"github.com/team-loco/loco/tools/internal/devicelogin"
)

var (
	errDeviceApproval  = errors.New("approve the device code with the fixture IdP token failed")
	errAdminsUnclaimed = errors.New("status reports configured admins nobody claimed after sign-in")
)

var (
	errFixtureCAInvalid             = errors.New("invalid fixture CA certificate")
	errFixtureCAPEM                 = errors.New("fixture CA certificate is not PEM")
	errFreshOperatorIdentityChanged = errors.New(
		"fresh operator apply replaced installation credentials or worker identity",
	)
	errHTTPSReadiness              = errors.New("application HTTPS readiness did not succeed")
	errInstallerCredentialsMissing = errors.New("installer created no owned credential Secrets")
	errMissingIssuerAccepted       = errors.New("initial apply unexpectedly accepted a missing issuer")
	errNetworkPolicyInputs         = errors.New("network-policy fixture inputs are missing")
	errNetworkPolicyProbeAllowed   = errors.New(
		"build namespace NetworkPolicy does not block Kubernetes API access",
	)
	errOrganizationIDMissing  = errors.New("organization creation returned no ID")
	errReapplyIdentityChanged = errors.New(
		"repeat apply replaced installation credentials or worker identity",
	)
	errStagingEnvironmentCreate = errors.New("create staging environment failed")
	errUnrestrictedProbeFailed  = errors.New("unrestricted fixture cannot reach the Kubernetes API")
	errWorkerIdentityIncomplete = errors.New("worker identity is incomplete")
	errWorkspaceIDMissing       = errors.New("workspace creation returned no ID")
)

const (
	fixtureEmail    = "dev@loco.test"
	fixturePassword = "loco-dev"
	// fixtureIssuer and fixtureAPIURL match tests/e2e/fixtures/installation/loco-install.yaml.
	fixtureIssuer = "http://dex.installation-fixtures.svc.cluster.local:5556/dex"
	fixtureAPIURL = "http://api.installation.test"
	fixtureClient = "loco"
	// formPassword names both the OAuth password grant and its password field.
	formPassword           = "password"
	clientFile             = "/installation/loco-install.yaml"
	clientTimeout          = 45 * time.Minute
	fixtureWorkloadTimeout = 5 * time.Minute
	httpsReadyTimeout      = 2 * time.Minute
	fixtureResponseLimit   = 1 << 20
	fixtureKubernetesHost  = "kubernetes.default.svc"
	fixtureKubernetesPort  = "443"
)

func clientAcceptance(ctx context.Context) (result error) {
	operatorPhase(phasePreflight)
	audit := newLeakAudit()
	stopAudit, err := audit.listen(auditSocket)
	if err != nil {
		return err
	}
	defer func() { result = errors.Join(result, stopAudit()) }()
	ctx = context.WithValue(ctx, auditKey{}, audit)
	defer func() {
		// Preserve the original failing phase when the final audit follows a failure.
		if result == nil {
			operatorPhase(phaseCompletionAudit)
		}
		// Inspect even after a failed command, before any raw provider diagnostics escape.
		inspectionCtx, cancelInspection := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
		defer cancelInspection()
		result = errors.Join(result, audit.inspect(inspectionCtx, "completion"))
	}()
	ctx, cancel := context.WithTimeout(ctx, clientTimeout)
	defer cancel()
	if err := clientFlow(ctx, audit); err != nil {
		return err
	}
	operatorPhase(phaseRecovery)
	if err := recoveryDrill(ctx); err != nil {
		return err
	}
	operatorPhase(phaseTLSMinimum)
	if err := verifyTLSMinimum(ctx); err != nil {
		return err
	}
	fmt.Println("Installation passed: sign-in, source build, HTTPS, preserved identity and recovery.")
	return nil
}

func clientFlow(ctx context.Context, audit *leakAudit) error {
	for _, operation := range []string{"validate", "plan"} {
		if _, checkErr := locoadm(ctx, operation, "--file", clientFile); checkErr != nil {
			return fmt.Errorf("initial %s: %w", operation, checkErr)
		}
	}
	operatorPhase(phaseControlPlane)
	if _, checkErr := kubectl(ctx, "get", "clusterissuer", "installation-ca"); checkErr != nil {
		if _, checkErr := locoadm(ctx, "apply", "--file", clientFile); checkErr == nil {
			return errMissingIssuerAccepted
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
	operatorPhase(phasePreLoginAudit)
	if checkErr := audit.inspect(ctx, "before login"); checkErr != nil {
		return checkErr
	}
	operatorPhase(phaseLogin)
	if checkErr := signIn(ctx, approveDeviceCode); checkErr != nil {
		return checkErr
	}
	operatorPhase(phasePostLoginAudit)
	if checkErr := audit.inspect(ctx, "login complete"); checkErr != nil {
		return checkErr
	}
	if checkErr := requireAdminsClaimed(ctx); checkErr != nil {
		return checkErr
	}
	operatorPhase(phaseWorkers)
	if _, targetErr := locoadm(ctx, "cluster", "list"); targetErr != nil {
		return fmt.Errorf("saved login target: %w", targetErr)
	}
	if _, checkErr := locoadm(ctx, "cluster", "attach", "--file", clientFile); checkErr != nil {
		return checkErr
	}
	if dnsErr := configureGatewayDNS(ctx, workerNamespace, true); dnsErr != nil {
		return dnsErr
	}
	if checkErr := audit.inspect(ctx, "workers attached"); checkErr != nil {
		return checkErr
	}
	operatorPhase(phaseRepeatApply)
	before, err := identitySnapshot(ctx)
	if err != nil {
		return err
	}
	if checkErr := applyAndAttach(ctx); checkErr != nil {
		return checkErr
	}
	if checkErr := audit.inspect(ctx, "repeat apply complete"); checkErr != nil {
		return checkErr
	}
	after, err := identitySnapshot(ctx)
	if err != nil {
		return err
	}
	if before != after {
		return errReapplyIdentityChanged
	}
	if checkErr := freshOperator(ctx, audit, before); checkErr != nil {
		return checkErr
	}
	operatorPhase(phaseWorkloads)
	if checkErr := deployFixture(ctx); checkErr != nil {
		return checkErr
	}
	// The build controller creates namespace policies before its first real Build Job.
	operatorPhase(phaseNetworkPolicy)
	if checkErr := probeNetworkPolicy(ctx); checkErr != nil {
		return checkErr
	}
	return nil
}

// freshOperator checks that a fresh local credential store signs in again and reapplies
// without replacing installation credentials or the worker identity.
func freshOperator(ctx context.Context, audit *leakAudit, before string) error {
	operatorPhase(phaseFreshHome)
	originalHome := os.Getenv("HOME")
	if checkErr := os.Setenv("HOME", "/tmp/operator-second"); checkErr != nil {
		return checkErr
	}
	if checkErr := signIn(ctx, approveDeviceCode); checkErr != nil {
		return fmt.Errorf("sign in from fresh operator state: %w", checkErr)
	}
	operatorPhase(phasePostLoginAudit)
	if checkErr := audit.inspect(ctx, "fresh login complete"); checkErr != nil {
		return checkErr
	}
	operatorPhase(phaseFreshHome)
	if checkErr := applyAndAttach(ctx); checkErr != nil {
		return fmt.Errorf("apply from fresh operator state: %w", checkErr)
	}
	if checkErr := audit.inspect(ctx, "fresh apply complete"); checkErr != nil {
		return checkErr
	}
	after, err := identitySnapshot(ctx)
	if err != nil {
		return err
	}
	if before != after {
		return errFreshOperatorIdentityChanged
	}
	return os.Setenv("HOME", originalHome)
}

func applyAndAttach(ctx context.Context) error {
	if _, err := locoadm(ctx, "apply", "--file", clientFile); err != nil {
		return err
	}
	_, err := locoadm(ctx, "cluster", "attach", "--file", clientFile)
	return err
}

func requireAdminsClaimed(ctx context.Context) error {
	output, err := locoadm(ctx, "status", "--file", clientFile)
	if err != nil {
		return err
	}
	var status struct {
		Admins struct {
			Checked   bool     `json:"checked"`
			Unclaimed []string `json:"unclaimed"`
		} `json:"admins"`
	}
	if json.Unmarshal(output, &status) != nil || !status.Admins.Checked || len(status.Admins.Unclaimed) != 0 {
		return errAdminsUnclaimed
	}
	return nil
}

// signIn keeps installation diagnostics and credential auditing at the CLI boundary.
func signIn(ctx context.Context, approve func(context.Context, string) error) (result error) {
	var stderr boundedOutput
	defer func() {
		if result != nil {
			fmt.Fprintln(os.Stderr, loginFailurePrefix+classifyLoginFailure(stderr.Bytes()))
		}
	}()
	cmd := exec.CommandContext(ctx, "locoadm", "login", "--file", clientFile, "--device")
	cmd.Stderr = &stderr
	var transcript io.Writer = io.Discard
	if audit, auditing := ctx.Value(auditKey{}).(*leakAudit); auditing {
		cmd.Stderr = io.MultiWriter(&stderr, auditWriter{audit, "CLI output (stderr)"})
		transcript = auditWriter{audit, "CLI output (stdout)"}
	}
	return devicelogin.Run(ctx, cmd, transcript, func(ctx context.Context, code string) error {
		operatorPhase(phaseLoginApprove)
		if err := approve(ctx, code); err != nil {
			return err
		}
		operatorPhase(phaseLoginApproved)
		return nil
	})
}

func approveDeviceCode(ctx context.Context, code string) error {
	token, err := fixtureIDToken(ctx, fixtureIssuer)
	if err != nil {
		return err
	}
	return devicelogin.Approve(ctx, fixtureAPIURL, token, code)
}

func fixtureIDToken(ctx context.Context, issuer string) (string, error) {
	token, err := (devicelogin.Identity{Issuer: issuer, ClientID: fixtureClient, Email: fixtureEmail, Password: fixturePassword}).Token(ctx)
	if err != nil {
		return "", err
	}
	if audit, auditing := ctx.Value(auditKey{}).(*leakAudit); auditing {
		audit.add([]byte(token))
	}
	return token, nil
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
		return "", errInstallerCredentialsMissing
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
		return "", errWorkerIdentityIncomplete
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
	organization, checkErr := command(ctx, "", nil, "loco", "org", "create", "installation-e2e")
	if checkErr != nil {
		return checkErr
	}
	organizationID := regexp.MustCompile(`[a-f0-9]{8}-[a-f0-9-]{27}`).FindString(string(organization))
	if organizationID == "" {
		return errOrganizationIDMissing
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
		return errWorkspaceIDMissing
	}
	if _, checkErr := command(ctx, "", nil, "loco", "use", organizationID+"/"+workspaceID); checkErr != nil {
		return checkErr
	}

	api, apiErr := fixtureAPI()
	if apiErr != nil {
		return apiErr
	}
	environment, createErr := api.Environment.CreateEnvironment(
		ctx,
		&environmentv1.CreateEnvironmentRequest{
			WorkspaceId: workspaceID,
			Name:        "staging",
			Type:        environmentv1.EnvironmentType_ENVIRONMENT_TYPE_STAGING,
		},
	)
	if createErr != nil {
		return errStagingEnvironmentCreate
	}
	environmentID := environment.GetEnvironmentId()
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
	operatorPhase(phaseSourceDeploy)
	deployCtx, cancelDeploy := context.WithTimeout(ctx, fixtureWorkloadTimeout)
	_, deployErr := command(deployCtx, app, nil, "loco", "deploy", "--yes")
	cancelDeploy()
	if deployErr != nil {
		return fmt.Errorf("source build and deploy: %w", deployErr)
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
	operatorPhase(phaseSourceHTTPS)
	if err := waitHTTPS(ctx, client, request); err != nil {
		return err
	}
	operatorPhase(phaseSourceVerify)
	verifyTimeout := fixtureWorkloadTimeout.String()
	if _, verifyErr := locoadm(
		ctx,
		"verify",
		"--timeout", verifyTimeout,
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
		return nil, errFixtureCAInvalid
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
		return nil, errFixtureCAPEM
	}
	return roots, nil
}

func probeNetworkPolicy(ctx context.Context) (result error) {
	image := os.Getenv("E2E_PROBE_IMAGE")
	namespace := os.Getenv("E2E_BUILD_NAMESPACE")
	if image == "" || namespace == "" {
		return errNetworkPolicyInputs
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
		defer cancel()
		for _, target := range []string{fixtureNamespace, namespace} {
			if _, err := kubectl(
				cleanupCtx,
				"-n",
				target,
				"delete",
				"pod",
				"installation-network-probe",
				"--ignore-not-found",
			); err != nil {
				result = errors.Join(result, fmt.Errorf("cleanup network-policy probe in %s: %w", target, err))
			}
		}
	}()
	if err := ctx.Err(); err != nil {
		return err
	}
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
		fixtureKubernetesHost,
		fixtureKubernetesPort,
	); checkErr != nil {
		return errUnrestrictedProbeFailed
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
			fixtureKubernetesHost,
			fixtureKubernetesPort,
		); checkErr != nil {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return errNetworkPolicyProbeAllowed
		case <-retry.C:
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
	deadline, cancel := context.WithTimeout(ctx, httpsReadyTimeout)
	defer cancel()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		response, err := client.Do(request.Clone(deadline))
		if err == nil {
			_, readErr := io.Copy(io.Discard, io.LimitReader(response.Body, fixtureResponseLimit))
			closeErr := response.Body.Close()
			if response.StatusCode == http.StatusOK && readErr == nil && closeErr == nil {
				return nil
			}
		}
		select {
		case <-deadline.Done():
			return errHTTPSReadiness
		case <-ticker.C:
		}
	}
}
