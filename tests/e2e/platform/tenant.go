package platform

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"sigs.k8s.io/yaml"

	environmentv1 "github.com/team-loco/loco/gen/go/loco/environment/v1"
	orgv1 "github.com/team-loco/loco/gen/go/loco/org/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	workspacev1 "github.com/team-loco/loco/gen/go/loco/workspace/v1"
	"github.com/team-loco/loco/internal/locofile"
	"github.com/team-loco/loco/k8sapi/v1alpha1"
)

// Production is the name of the environment every new workspace has.
const Production = "production"

var nameSeparators = regexp.MustCompile(`[^a-z0-9]+`)

// Workspace is a workspace of a test's organization, with a CLI home whose scope is the workspace.
type Workspace struct {
	p             *Platform
	OrgID         string
	OrgName       string
	ID            string
	Name          string
	EnvironmentID string
	// Namespace is the namespace of the workspace's workloads.
	Namespace string
	// Home is the home directory the workspace's CLI runs with.
	Home string
}

// NewTenant creates an organization and a workspace for the test, as the application user, so
// tests running side by side never share a workspace or an organization.
func (p *Platform) NewTenant(t *testing.T) *Workspace {
	t.Helper()
	name := "e2e-" + strings.Trim(
		nameSeparators.ReplaceAllString(strings.ToLower(strings.TrimPrefix(t.Name(), "Test")), "-"),
		"-",
	)
	// Kept platforms retain earlier runs; name-based CLI lookups must stay unambiguous.
	orgName := name + "-" + uuid.NewString()
	org, err := p.User.Org.CreateOrg(t.Context(), &orgv1.CreateOrgRequest{Name: new(orgName)})
	if err != nil {
		t.Fatalf("create organization %s: %v", orgName, err)
	}
	return p.newWorkspace(t, org.GetOrgId(), orgName, name)
}

// NewWorkspace creates another workspace in the same organization.
func (w *Workspace) NewWorkspace(t *testing.T, name string) *Workspace {
	t.Helper()
	return w.p.newWorkspace(t, w.OrgID, w.OrgName, name)
}

func (p *Platform) newWorkspace(t *testing.T, orgID, orgName, name string) *Workspace {
	t.Helper()
	created, err := p.User.Workspace.CreateWorkspace(
		t.Context(),
		&workspacev1.CreateWorkspaceRequest{OrgId: orgID, Name: name},
	)
	if err != nil {
		t.Fatalf("create workspace %s: %v", name, err)
	}
	environments, err := p.User.Environment.ListEnvironments(
		t.Context(),
		&environmentv1.ListEnvironmentsRequest{WorkspaceId: created.GetWorkspaceId()},
	)
	if err != nil {
		t.Fatal(err)
	}
	w := &Workspace{p: p, OrgID: orgID, OrgName: orgName, ID: created.GetWorkspaceId(), Name: name,
		Namespace: p.WorkspaceNamespace(created.GetWorkspaceId()), Home: t.TempDir()}
	for _, environment := range environments.GetEnvironments() {
		if environment.GetType() == environmentv1.EnvironmentType_ENVIRONMENT_TYPE_PRODUCTION {
			w.EnvironmentID = environment.GetId()
		}
	}
	if w.EnvironmentID == "" {
		t.Fatalf("workspace %s has no production environment", name)
	}
	w.WriteCLIConfig(t)
	return w
}

// WriteCLIConfig writes the CLI's credentials and its scope of the workspace into Home.
func (w *Workspace) WriteCLIConfig(tb testing.TB) {
	tb.Helper()
	if err := writeCredentials(w.Home, w.p.userSession); err != nil {
		tb.Fatal(err)
	}
	config := fmt.Sprintf(
		"currentScope: default\nscopes:\n  default:\n"+
			"    organization:\n      name: %s\n      id: %q\n    workspace:\n      name: %s\n      id: %q\n",
		w.OrgName,
		w.OrgID,
		w.Name,
		w.ID,
	)
	if err := os.WriteFile(w.ConfigPath(), []byte(config), 0o600); err != nil {
		tb.Fatal(err)
	}
}

// ConfigPath is the CLI's scope file in Home.
func (w *Workspace) ConfigPath() string {
	return filepath.Join(w.Home, ".loco", "config.yaml")
}

// CLIResult is the output of one CLI run.
type CLIResult struct {
	Stdout string
	Stderr string
	Err    error
}

// Output is the run's standard output and standard error, for failure messages.
func (r CLIResult) Output() string {
	return r.Stdout + r.Stderr
}

// Loco runs the loco CLI as the application user in dir, scoped to the workspace, with env added.
func (w *Workspace) Loco(ctx context.Context, dir string, env []string, args ...string) CLIResult {
	cmd := exec.CommandContext(ctx, filepath.Join(w.p.binDir, "loco"), args...)
	cmd.Dir = dir
	cmd.Env = append(
		append(append([]string{}, w.p.env...), "HOME="+w.Home, "LOCO_CREDENTIAL_STORE=file", "LOCO_HOST="+w.p.APIURL),
		env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return CLIResult{Stdout: stdout.String(), Stderr: stderr.String(), Err: err}
}

// MustLoco runs the loco CLI and fails the test unless it succeeds.
func (w *Workspace) MustLoco(tb testing.TB, dir string, args ...string) CLIResult {
	tb.Helper()
	result := w.Loco(tb.Context(), dir, nil, args...)
	if result.Err != nil {
		w.p.LogWorkspaceRuntime(tb, w.Namespace)
		tb.Fatalf("loco %s: %v\n%s", strings.Join(args, " "), result.Err, result.Output())
	}
	return result
}

// Resource looks a resource of the workspace up by name.
func (w *Workspace) Resource(ctx context.Context, name string) (*resourcev1.Resource, error) {
	response, err := w.p.User.Resource.GetResource(ctx, &resourcev1.GetResourceRequest{
		Key: &resourcev1.GetResourceRequest_NameKey{
			NameKey: &resourcev1.GetResourceNameKey{WorkspaceId: w.ID, Name: name},
		},
	})
	if err != nil {
		return nil, err
	}
	return response.GetResource(), nil
}

// ResourceID returns the ID of the workspace's resource name, failing the test without one.
func (w *Workspace) ResourceID(tb testing.TB, name string) string {
	tb.Helper()
	resource, err := w.Resource(tb.Context(), name)
	if err != nil {
		tb.Fatalf("get resource %s: %v", name, err)
	}
	return resource.GetId()
}

// IsNotFound reports whether err is a NotFound error from the API.
func IsNotFound(err error) bool {
	return connect.CodeOf(err) == connect.CodeNotFound
}

// Application returns the Application of a resource.
func (p *Platform) Application(ctx context.Context, resourceID string) (*v1alpha1.Application, error) {
	var application v1alpha1.Application
	if err := p.Get(ctx, &application, "-n", locoNamespace, "application", "resource-"+resourceID); err != nil {
		return nil, err
	}
	return &application, nil
}

// WriteLocoFile writes file as dir/loco.yaml.
func WriteLocoFile(tb testing.TB, dir string, file locofile.File) {
	tb.Helper()
	file.Version = locofile.Version
	data, err := yaml.Marshal(file)
	if err != nil {
		tb.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		tb.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, locofile.FileName), data, 0o600); err != nil {
		tb.Fatal(err)
	}
}

// ReadLocoFile reads dir/loco.yaml.
func ReadLocoFile(tb testing.TB, dir string) locofile.File {
	tb.Helper()
	data, err := os.ReadFile(filepath.Join(dir, locofile.FileName))
	if err != nil {
		tb.Fatal(err)
	}
	var file locofile.File
	if err := yaml.Unmarshal(data, &file); err != nil {
		tb.Fatal(err)
	}
	return file
}

// Service is a one-region service as most suites deploy it: a 100m, 128Mi replica in the run's
// region, routed at / with a 60s idle timeout.
func Service(port int32, domains ...string) locofile.Service {
	return locofile.Service{
		Port:    new(port),
		Routing: &locofile.Routing{PathPrefix: "/", IdleTimeout: new(int32(60))},
		Domains: domains,
		Regions: map[string]locofile.Region{clusterRegion: OneReplica()},
	}
}

// OneReplica is a 100m, 128Mi region with one replica.
func OneReplica() locofile.Region {
	return locofile.Region{
		CPU:      "100m",
		Memory:   "128Mi",
		Replicas: &locofile.Replicas{Min: new(int32(1)), Max: new(int32(1))},
	}
}

// Health checks path every 5s with a 3s timeout and three failures.
func Health(path string) *locofile.Health {
	return &locofile.Health{
		Path:               path,
		Interval:           new(int32(5)),
		Timeout:            new(int32(3)),
		FailThreshold:      new(int32(3)),
		StartupGracePeriod: new(int32(0)),
	}
}

// ImageService runs image on port 8080 with a health check on /.
func ImageService(image string, domains ...string) locofile.Service {
	service := Service(8080, domains...)
	service.Image = image
	service.Health = Health("/")
	return service
}

// SourceService builds deploy/Dockerfile and serves port 8080 with a health check on /healthz.
func SourceService(domains ...string) locofile.Service {
	service := Service(8080, domains...)
	service.Dockerfile = "deploy/Dockerfile"
	service.Health = Health("/healthz")
	return service
}

// Host is a workspace-owned hostname under the platform domain.
func (w *Workspace) Host(name string) string {
	return name + "-" + w.ID + "." + platformDomain
}

// CopyFixture copies a fixture of tests/e2e/fixtures into dir.
func (p *Platform) CopyFixture(tb testing.TB, fixture, dir string) {
	tb.Helper()
	if err := os.CopyFS(dir, os.DirFS(filepath.Join(p.root, "tests", "e2e", "fixtures", fixture))); err != nil {
		tb.Fatal(err)
	}
}

// Root is the repository root.
func (p *Platform) Root() string {
	return p.root
}
