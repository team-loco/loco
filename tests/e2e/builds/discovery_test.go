package builds

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	"github.com/team-loco/loco/internal/locofile"
	"github.com/team-loco/loco/tests/e2e/platform"
)

const (
	aliasTimeout         = time.Minute
	discoveryDumpTimeout = time.Minute
	cartName             = "cart"
	callerName           = "caller"
	workspaceOne         = "workspace-one"
)

// TestDiscovery deploys private HTTP and TCP services and checks how other apps reach them: by
// logical name and port, after a runtime user change, a rename and a delete, and never across
// workspaces.
func TestDiscovery(t *testing.T) {
	t.Parallel()
	w := p.NewTenant(t)
	t.Cleanup(func() {
		if t.Failed() {
			dumpDiscovery(t, w.Namespace)
		}
	})
	dir := t.TempDir()
	p.CopyFixture(t, "tcp-app", filepath.Join(dir, "services", "payment"))
	cart := filepath.Join(dir, "services", cartName)
	p.CopyFixture(t, "build-app", cart)
	dockerfile := filepath.Join(cart, "deploy", "Dockerfile")
	data, err := os.ReadFile(dockerfile)
	if err != nil {
		t.Fatal(err)
	}
	// A named image USER, which runAsUser must override.
	if err := os.WriteFile(
		dockerfile,
		[]byte(strings.ReplaceAll(string(data), "USER 65532:65532", "USER nonroot")),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	file := locofile.File{Partial: "discovery", Services: map[string]locofile.Service{
		cartName: {
			Context:    "services/cart",
			Dockerfile: buildAppDockerfile,
			Env:        map[string]string{"MESSAGE": workspaceOne},
			RunAsUser:  new(int64(1000)),
			RunAsGroup: new(int64(1000)),
			Port:       new(int32(8080)),
			Regions:    map[string]locofile.Region{p.Region: platform.OneReplica()},
		},
		callerName: {
			Image:   p.PublicImage,
			Port:    new(int32(8080)),
			Regions: map[string]locofile.Region{p.Region: platform.OneReplica()},
		},
		"payment": {Context: "services/payment", Dockerfile: buildAppDockerfile, Port: new(int32(50051)),
			Regions: map[string]locofile.Region{p.Region: platform.OneReplica()}},
	}}
	platform.WriteLocoFile(t, dir, file)

	if !t.Run("deploys private HTTP and TCP services", func(t *testing.T) {
		w.MustLoco(t, dir, "deploy", "--yes", "--wait")
		var deployment appsv1.Deployment
		p.MustGet(t, &deployment, "-n", w.Namespace, "deployment", "resource-"+w.ResourceID(t, "payment"))
		container := deployment.Spec.Template.Spec.Containers[0]
		if container.LivenessProbe != nil ||
			container.ReadinessProbe != nil && container.ReadinessProbe.HTTPGet != nil {
			t.Error("payment, without a health check, has an HTTP probe")
		}
	}) {
		return
	}

	d := &discovery{w: w}
	t.Run("apps use logical names and configured ports", d.logicalNames)

	t.Run("a runtime user update reuses the image", func(t *testing.T) {
		before := workspaceBuilds(t, w)
		cart := file.Services[cartName]
		cart.RunAsUser = new(int64(1001))
		file.Services[cartName] = cart
		platform.WriteLocoFile(t, dir, file)
		w.MustLoco(t, dir, "infra", "apply", "--yes")
		platform.Eventually(t, 2*time.Minute, "new pods with the updated UID", func(ctx context.Context) error {
			out, err := callHTTP(ctx, w, cartName, "/identity")
			if err != nil {
				return err
			}
			if strings.TrimSpace(out) != "1001:1000" {
				return fmt.Errorf("identity %q", out)
			}
			return nil
		})
		if after := workspaceBuilds(t, w); after != before {
			t.Errorf("the runtime user update started %d builds", after-before)
		}
	})

	t.Run("a rename moves the alias without a build", d.rename)

	t.Run("a delete removes only its alias", func(t *testing.T) {
		if _, err := p.User.Resource.DeleteResource(
			t.Context(),
			&resourcev1.DeleteResourceRequest{ResourceId: w.ResourceID(t, "checkout")},
		); err != nil {
			t.Fatal(err)
		}
		waitAlias(t, w, "checkout", false)
		if found, err := p.Exists(t.Context(), "-n", w.Namespace, "service", cartName); err != nil || !found {
			t.Errorf("cart lost its logical endpoint (%v)", err)
		}
		if out, err := callHTTP(
			t.Context(),
			w,
			"cart:8080",
			"/",
		); err != nil ||
			!strings.Contains(out, workspaceOne) {
			t.Errorf("cart is unreachable after another service's delete: %q (%v)", out, err)
		}
	})

	t.Run("namespace-qualified names reach only their workspace", d.otherWorkspace)
}

func callHTTP(ctx context.Context, w *platform.Workspace, target, path string) (string, error) {
	caller, err := w.Resource(ctx, callerName)
	if err != nil {
		return "", err
	}
	return p.Kubectl(ctx, "-n", w.Namespace, "exec", "deployment/resource-"+caller.GetId(), "--",
		"curl", "--fail", "--silent", "--show-error", "--max-time", "5", "http://"+target+path)
}

func callTCP(ctx context.Context, w *platform.Workspace, target string) (string, error) {
	caller, err := w.Resource(ctx, callerName)
	if err != nil {
		return "", err
	}
	host, port, _ := strings.Cut(target, ":")
	return p.Kubectl(ctx, "-n", w.Namespace, "exec", "deployment/resource-"+caller.GetId(), "--",
		"sh", "-c", `printf "private-tcp-ok\n" | nc -w 5 "$1" "$2"`, "--", host, port)
}

func waitAlias(t *testing.T, w *platform.Workspace, alias string, present bool) {
	t.Helper()
	platform.Eventually(
		t,
		aliasTimeout,
		fmt.Sprintf("Service %s present=%v", alias, present),
		func(ctx context.Context) error {
			found, err := p.Exists(ctx, "-n", w.Namespace, "service", alias)
			if err == nil && found != present {
				return fmt.Errorf("Service %s present=%v", alias, found)
			}
			return err
		},
	)
}

func workspaceBuilds(t *testing.T, w *platform.Workspace) int {
	t.Helper()
	var count int
	const query = "SELECT count(*) FROM builds b JOIN resources r ON r.id = b.resource_id WHERE r.workspace_id = $1"
	if err := p.DB.QueryRow(t.Context(), query, w.ID).
		Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

// dumpDiscovery preserves routing and workload diagnostics before failure cleanup removes them.
func dumpDiscovery(t *testing.T, namespace string) {
	t.Helper()
	p.LogKubectl(t,
		"-n "+namespace+" get services,endpointslices,networkpolicies,pods -o wide",
		"-n "+namespace+" get pods -o=custom-columns=NAME:.metadata.name,PHASE:.status.phase,"+
			"REASON:.status.containerStatuses[*].state.waiting.reason,"+
			"MESSAGE:.status.containerStatuses[*].state.waiting.message",
		"-n "+namespace+" get events --sort-by=.metadata.creationTimestamp",
		"-n "+namespace+" get networkpolicies -o yaml",
		"-n kube-system get pods -l k8s-app=kube-dns -o wide",
		"-n kube-system logs -l k8s-app=kube-dns --all-containers --prefix --tail=100",
	)
	ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), discoveryDumpTimeout)
	defer cancel()
	out, err := p.Kubectl(ctx, "-n", namespace, "get", "secret", "loco-registry-pull",
		"-o=custom-columns=NAME:.metadata.name,UID:.metadata.uid,CREATED:.metadata.creationTimestamp,"+
			"DELETING:.metadata.deletionTimestamp,TYPE:.type,RESOURCEVERSION:.metadata.resourceVersion")
	t.Logf("registry pull secret metadata:\n%s%v", out, err)
	out, err = p.Command(ctx, "docker", "exec", p.Node(), "journalctl", "-u", "kubelet",
		"--since=-5min", "--no-pager", "--output=short-iso-precise", "--lines=200", "--grep="+namespace)
	t.Logf("workspace kubelet messages:\n%s%v", out, err)
}

// discovery is the workspace TestDiscovery deploys into.
type discovery struct {
	w *platform.Workspace
}

func (d *discovery) logicalNames(t *testing.T) {
	w := d.w
	for _, check := range []struct{ what, target, path, want string }{
		{"a numeric UID overrides a named image USER", cartName, "/identity", "1000:1000"},
		{"the qualified name without a port reaches cart", "cart." + w.Namespace + ".svc.cluster.local", "/", workspaceOne},
		{"the short name reaches cart", cartName, "/", workspaceOne},
		{"cart:8080 reaches cart over HTTP", "cart:8080", "/", workspaceOne},
	} {
		if out, err := callHTTP(
			t.Context(),
			w,
			check.target,
			check.path,
		); err != nil ||
			!strings.Contains(out, check.want) {
			t.Errorf("%s: got %q (%v), want %q", check.what, out, err, check.want)
		}
	}
	if out, err := callTCP(
		t.Context(),
		w,
		"payment:50051",
	); err != nil ||
		!strings.Contains(out, "private-tcp-ok") {
		t.Errorf("payment:50051 over TCP: got %q (%v)", out, err)
	}
	if out, err := callTCP(
		t.Context(),
		w,
		"resource-"+w.ResourceID(t, "payment")+":80",
	); err != nil ||
		!strings.Contains(out, "private-tcp-ok") {
		t.Errorf("the resource ID endpoint on port 80: got %q (%v)", out, err)
	}
	var service corev1.Service
	p.MustGet(t, &service, "-n", w.Namespace, "service", "payment")
	if protocol := service.Spec.Ports[0].Protocol; protocol != corev1.ProtocolTCP {
		t.Errorf("payment's Service protocol %s, want TCP", protocol)
	}
}

func (d *discovery) rename(t *testing.T) {
	w := d.w
	before := workspaceBuilds(t, w)
	if _, err := p.User.Resource.UpdateResource(t.Context(), &resourcev1.UpdateResourceRequest{
		ResourceId: w.ResourceID(t, "payment"), Name: new("checkout"),
	}); err != nil {
		t.Fatal(err)
	}
	waitAlias(t, w, "checkout", true)
	waitAlias(t, w, "payment", false)
	platform.Eventually(t, aliasTimeout, "the renamed TCP endpoint", func(ctx context.Context) error {
		out, err := callTCP(ctx, w, "checkout:50051")
		if err == nil && !strings.Contains(out, "private-tcp-ok") {
			return fmt.Errorf("checkout:50051 answered %q", out)
		}
		return err
	})
	if after := workspaceBuilds(t, w); after != before {
		t.Errorf("the rename started %d builds", after-before)
	}
}

func (d *discovery) otherWorkspace(t *testing.T) {
	w := d.w
	other := w.NewWorkspace(t, w.Name+"-two")
	t.Cleanup(func() {
		if t.Failed() {
			dumpDiscovery(t, other.Namespace)
		}
	})
	otherDir := t.TempDir()
	p.CopyFixture(t, "build-app", filepath.Join(otherDir, "services", cartName))
	platform.WriteLocoFile(
		t,
		otherDir,
		locofile.File{Partial: "discovery-two", Services: map[string]locofile.Service{
			cartName: {
				Context:    "services/cart",
				Dockerfile: buildAppDockerfile,
				Port:       new(int32(8080)),
				Env: map[string]string{
					"MESSAGE": "workspace-two",
				},
				Regions: map[string]locofile.Region{p.Region: platform.OneReplica()},
			},
			callerName: {
				Image:   p.PublicImage,
				Port:    new(int32(8080)),
				Regions: map[string]locofile.Region{p.Region: platform.OneReplica()},
			},
		}},
	)
	other.MustLoco(t, otherDir, "deploy", "--yes", "--wait")
	if out, err := callHTTP(
		t.Context(),
		other,
		"cart."+other.Namespace+".svc.cluster.local",
		"/",
	); err != nil ||
		!strings.Contains(out, "workspace-two") {
		t.Errorf("the second workspace's app cannot reach its own cart: %q (%v)", out, err)
	}
	if out, err := callHTTP(
		t.Context(),
		w,
		"cart."+w.Namespace+".svc.cluster.local",
		"/",
	); err != nil ||
		!strings.Contains(out, workspaceOne) {
		t.Errorf("the first workspace cannot reach its own cart after the second deploys one: %q (%v)", out, err)
	}
	var service corev1.Service
	p.MustGet(t, &service, "-n", other.Namespace, "service", cartName)
	if out, err := callHTTP(t.Context(), w, service.Spec.ClusterIP, "/"); err == nil {
		t.Errorf("the first workspace reaches the second workspace's cart at %s: %q", service.Spec.ClusterIP, out)
	}
}
