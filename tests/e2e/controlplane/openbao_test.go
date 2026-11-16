package controlplane

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/team-loco/loco/tests/e2e/platform"
)

const (
	secretResource = "00000000-0000-7000-8000-0000000000a1"
	openbaoPod     = release + "-openbao-0"
	initSecret     = release + "-openbao-init"
	recoveryKeys   = "recovery-keys"
	generateToken  = "generate-root-token"
	leaderTimeout  = 2 * time.Minute
	loginTimeout   = time.Minute
	snapshotJob    = "openbao-snapshot-e2e"
	kubectlExec    = "exec"
)

var errNoLeader = errors.New("OpenBao elected no leader")

// checkUpgrade writes a secret, upgrades the chart and checks that OpenBao kept its init Secret,
// the API still writes secrets, the setup Job bound the API's role, and the only root token is the
// one the check generates from the recovery keys.
//
//nolint:thelper // a step of TestControlPlane
func (cp *controlPlane) checkUpgrade(t *testing.T) {
	token := persisted(t, "user-token", func() string { return "loco_s_" + randomHex(t, 24) })
	seed, err := os.ReadFile(c.Path("api", "seed", "e2e.sql"))
	if err != nil {
		t.Fatal(err)
	}
	cp.psql(t, string(seed))
	writer, err := os.ReadFile(c.Path("tests", "e2e", "fixtures", "control-plane", "secret-writer.sql"))
	if err != nil {
		t.Fatal(err)
	}
	cp.psql(t, string(writer), "-v", "resource="+secretResource, "-v", "token="+token)
	environment := cp.psql(t, "SELECT id FROM environments WHERE name = 'production' AND workspace_id = "+
		"(SELECT workspace_id FROM resources WHERE id = '"+secretResource+"')")
	cp.secrets = secretWriter{cp: cp, token: token, environment: environment}

	cp.secrets.setOnceTheAPILogsIn(t, "BEFORE_UPGRADE")
	before := cp.initSecret(t)
	if keys := slices.Sorted(maps.Keys(before)); !slices.Equal(keys, []string{generateToken, recoveryKeys}) {
		t.Fatalf("the init Secret holds %v, want only the recovery keys and the generate-root token", keys)
	}
	cp.recoveryKeys = before[recoveryKeys]
	credentials := map[string]map[string][]byte{}
	names := make([]string, 1, 1+len(cp.values.ClickHouse.ClickHouse.Users))
	names[0] = cp.values.ClickHouse.Operator.FullnameOverride
	for _, user := range cp.values.ClickHouse.ClickHouse.Users {
		names = append(names, user.PasswordSecretName)
	}
	for _, name := range names {
		var secret struct {
			Data map[string][]byte `json:"data"`
		}
		c.MustGet(t, &secret, "-n", cp.namespace, "secret", name)
		if len(secret.Data[passwordKey]) < 32 {
			t.Fatalf("credential %s has no generated password", name)
		}
		credentials[name] = secret.Data
	}
	cp.upgrade(t)
	for _, name := range names {
		var secret struct {
			Data map[string][]byte `json:"data"`
		}
		c.MustGet(t, &secret, "-n", cp.namespace, "secret", name)
		if !reflect.DeepEqual(credentials[name], secret.Data) {
			t.Fatalf("credential %s changed on upgrade", name)
		}
	}
	if cp.initSecret(t)[recoveryKeys] != cp.recoveryKeys {
		t.Fatal("the OpenBao setup Job replaced the init Secret on upgrade")
	}
	cp.secrets.set(t, "AFTER_UPGRADE")
	cp.checkSecretNames(t, "AFTER_UPGRADE BEFORE_UPGRADE")

	var role struct {
		Data struct {
			Names      []string `json:"bound_service_account_names"`
			Namespaces []string `json:"bound_service_account_namespaces"`
		} `json:"data"`
	}
	if err := json.Unmarshal(
		[]byte(cp.openbao(t, "bao read -format=json auth/kubernetes/role/loco-api")),
		&role,
	); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(role.Data.Names, []string{release + "-api"}) ||
		!slices.Equal(role.Data.Namespaces, []string{cp.namespace}) {
		t.Errorf("the loco-api role is bound to %v in %v, want the API ServiceAccount", role.Data.Names,
			role.Data.Namespaces)
	}
	roots := cp.openbao(t, `for accessor in $(bao list -format=table auth/token/accessors | tail -n +3); do `+
		`if bao token lookup -format=json -accessor "$accessor" | grep -q '"root"'; then echo root; fi; done`)
	if count := strings.Count(roots, "root"); count != 1 {
		t.Errorf("found %d root tokens, want only the generated one", count)
	}
}

// checkRestore restores a snapshot the CronJob uploaded into a reinstalled OpenBao, and checks that
// the API logs in again and reads the environment key it wrapped before the snapshot.
//
//nolint:thelper // a step of TestControlPlane
func (cp *controlPlane) checkRestore(t *testing.T) {
	ns := []string{"-n", cp.namespace}
	kubectl := func(args ...string) string {
		t.Helper()
		return c.MustKubectl(t, append(ns, args...)...)
	}
	kubectl("delete", "job", snapshotJob, "--ignore-not-found")
	kubectl("create", "job", snapshotJob, "--from=cronjob/"+release+"-openbao-snapshot")
	kubectl("wait", "--for=condition=complete", "job/"+snapshotJob, "--timeout", installTimeout.String())
	s3 := cp.values.OpenBao.Snapshots.S3
	objects := strings.Fields(kubectl("exec", "deployment/s3", "--", "find", "/data/"+s3.Bucket+"/"+s3.Prefix,
		"-name", "*.snap"))
	if len(objects) == 0 {
		t.Fatalf("the snapshot Job uploaded no snapshot to %s/%s", s3.Bucket, s3.Prefix)
	}
	slices.Sort(objects)
	snapshot := filepath.Join(c.Dir(), "openbao.snap")
	if err := os.WriteFile(snapshot, []byte(kubectl("exec", "deployment/s3", "--", "cat", objects[len(objects)-1])),
		0o600); err != nil {
		t.Fatal(err)
	}
	saved := cp.initSecret(t)

	kubectl("delete", "statefulset", release+"-openbao", "--wait")
	kubectl("delete", "pvc/data-"+openbaoPod, "secret/"+initSecret, "--wait")
	cp.upgrade(t)
	if cp.initSecret(t)[recoveryKeys] == cp.recoveryKeys {
		t.Fatal("the reinstalled OpenBao kept the recovery keys of the one it replaced")
	}
	kubectl("cp", snapshot, openbaoPod+":/tmp/openbao.snap")
	root := cp.generateRoot(t)
	kubectl("exec", openbaoPod, "--", "env", "BAO_TOKEN="+root, "bao", "operator", "raft", "snapshot", "restore",
		"-force", "/tmp/openbao.snap")
	kubectl("exec", openbaoPod, "--", "rm", "/tmp/openbao.snap")
	if _, err := c.KubectlInput(t.Context(), secretManifest(t, cp.namespace, initSecret, saved),
		"replace", "--force", "-f", "-"); err != nil {
		t.Fatal(err)
	}
	cp.upgrade(t)
	cp.secrets.setOnceTheAPILogsIn(t, "AFTER_RESTORE")
	cp.checkSecretNames(t, "AFTER_RESTORE AFTER_UPGRADE BEFORE_UPGRADE")
}

// psql runs SQL in the fixture Postgres and returns its unaligned output.
func (cp *controlPlane) psql(t *testing.T, sql string, args ...string) string {
	t.Helper()
	out, err := c.KubectlInput(t.Context(), sql, append([]string{"-n", cp.namespace, kubectlExec, "-i",
		"deployment/postgres", "--", "psql", "-U", "loco", "-d", "loco", "-v", "ON_ERROR_STOP=1", "-Atq"},
		args...)...)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(out)
}

func (cp *controlPlane) checkSecretNames(t *testing.T, want string) {
	t.Helper()
	names := cp.psql(t, "SELECT string_agg(name, ' ' ORDER BY name) FROM secrets WHERE resource_id = '"+
		secretResource+"'")
	if names != want {
		t.Fatalf("the resource has the secrets %q, want %q", names, want)
	}
}

// initSecret reads the decoded data of the OpenBao init Secret.
func (cp *controlPlane) initSecret(t *testing.T) map[string]string {
	t.Helper()
	var secret struct {
		Data map[string]string `json:"data"`
	}
	c.MustGet(t, &secret, "-n", cp.namespace, "secret", initSecret)
	decoded := map[string]string{}
	for key, value := range secret.Data {
		data, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			t.Fatal(err)
		}
		decoded[key] = string(data)
	}
	return decoded
}

// generateRoot generates a root token from the recovery keys once OpenBao has a leader.
func (cp *controlPlane) generateRoot(t *testing.T) string {
	t.Helper()
	platform.Eventually(t, leaderTimeout, "an OpenBao leader", func(ctx context.Context) error {
		out, err := c.Kubectl(ctx, "-n", cp.namespace, "exec", openbaoPod, "--", "bao", "read",
			"-field=leader_address", "sys/leader")
		if err == nil && strings.TrimSpace(out) == "" {
			return errNoLeader
		}
		return err
	})
	secret := cp.initSecret(t)
	generate := func(args ...string) string {
		t.Helper()
		return strings.TrimSpace(c.MustKubectl(t, append([]string{"-n", cp.namespace, "exec", openbaoPod, "--", "env",
			"BAO_TOKEN=" + secret[generateToken], "bao", "operator", "generate-root"}, args...)...))
	}
	otp := generate("-generate-otp")
	var started struct {
		Nonce string `json:"nonce"`
	}
	if err := json.Unmarshal([]byte(generate("-init", "-otp="+otp, "-format=json")), &started); err != nil {
		t.Fatal(err)
	}
	var progress struct {
		EncodedToken string `json:"encoded_token"`
	}
	for key := range strings.FieldsSeq(secret[recoveryKeys]) {
		if err := json.Unmarshal(
			[]byte(generate("-nonce="+started.Nonce, "-format=json", key)),
			&progress,
		); err != nil {
			t.Fatal(err)
		}
	}
	return generate("-decode="+progress.EncodedToken, "-otp="+otp)
}

// openbao runs a shell command in the OpenBao pod with a root token it revokes afterwards.
func (cp *controlPlane) openbao(t *testing.T, command string) string {
	t.Helper()
	root := cp.generateRoot(t)
	exec := func(args ...string) []string {
		return append([]string{"-n", cp.namespace, kubectlExec, openbaoPod, "--", "env", "BAO_TOKEN=" + root}, args...)
	}
	out, err := c.Kubectl(t.Context(), exec("sh", "-c", command)...)
	c.MustKubectl(t, exec("bao", "token", "revoke", "-self")...)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// secretWriter writes secrets of the fixture resource as the seeded user.
type secretWriter struct {
	cp          *controlPlane
	token       string
	environment string
}

func (s secretWriter) set(t *testing.T, name string) {
	t.Helper()
	if err := s.trySet(t, name); err != nil {
		t.Fatal(err)
	}
}

// setOnceTheAPILogsIn retries while the API logs in to OpenBao again.
func (s secretWriter) setOnceTheAPILogsIn(t *testing.T, name string) {
	t.Helper()
	platform.Eventually(t, loginTimeout, "SetSecrets "+name, func(context.Context) error { return s.trySet(t, name) })
}

func (s secretWriter) trySet(t *testing.T, name string) error {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"resourceId":    secretResource,
		"environmentId": s.environment,
		"values":        map[string]string{name: "value-of-" + name},
	})
	if err != nil {
		return err
	}
	response := s.cp.request(t, http.MethodPost, s.cp.values.ControlPlaneHostname,
		"/loco.secret.v1.SecretService/SetSecrets", string(body), bearer(s.token))
	if response.status != http.StatusOK {
		return fmt.Errorf("SetSecrets %s: status %d: %s", name, response.status, response.body)
	}
	return nil
}
