package apps

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	domainv1 "github.com/team-loco/loco/gen/go/loco/domain/v1"
	"github.com/team-loco/loco/internal/locofile"
	"github.com/team-loco/loco/tests/e2e/platform"
)

const routeTimeout = time.Minute

// TestIngress deploys an app with a domain and one without: the cluster's reported platform
// domain, routes and gateway policies, reachability from the gateway, the URLs the CLI prints, a
// lost domain, and a plan for a domain no cluster serves.
func TestIngress(t *testing.T) {
	t.Parallel()
	w := p.NewTenant(t)
	const public, private, unserved = "e2e-ingress-public", "e2e-ingress-private", "e2e-ingress-unserved"
	publicHost := platform.Host(public)

	t.Run("the cluster reports the domain its gateway serves", func(t *testing.T) {
		var seeded bool
		if err := p.DB.QueryRow(t.Context(), "SELECT to_regclass('platform_domains') IS NULL").
			Scan(&seeded); err != nil {
			t.Fatal(err)
		}
		if !seeded {
			t.Error("the install seeds a platform domains table")
		}
		platform.Eventually(t, time.Minute, "the agent to report "+p.Domain, func(ctx context.Context) error {
			var domains []string
			if err := p.DB.QueryRow(ctx, "SELECT domains FROM clusters WHERE id = $1", p.ClusterID).
				Scan(&domains); err != nil {
				return err
			}
			if !slices.Contains(domains, p.Domain) {
				return fmt.Errorf("the cluster reports %v", domains)
			}
			return nil
		})
	})

	deploy := func(t *testing.T, name string, domains ...string) string {
		t.Helper()
		dir := t.TempDir()
		platform.WriteLocoFile(
			t,
			dir,
			locofile.File{
				Partial:  name,
				Services: map[string]locofile.Service{name: platform.ImageService(p.PublicImage, domains...)},
			},
		)
		return w.MustLoco(t, dir, "deploy", "--yes").Stdout
	}

	t.Run("deploys with and without a domain", func(t *testing.T) {
		if out := deploy(t, public, publicHost); !strings.Contains(out, public+": https://"+publicHost) {
			t.Errorf("the CLI does not print the public app's URL:\n%s", out)
		}
		if out := deploy(t, private); !strings.Contains(out, private+" has no public URL") {
			t.Errorf("the CLI does not say the private app has no public URL:\n%s", out)
		}
		if domains := resourceDomains(t, w, private); len(domains) != 0 {
			t.Errorf("the private resource has domains %v", domains)
		}
	})

	t.Run("both apps become ready", func(t *testing.T) {
		w.WaitReady(t, public)
		w.WaitReady(t, private)
	})

	publicID, privateID := w.ResourceID(t, public), w.ResourceID(t, private)
	t.Run("only the public app is routed", func(t *testing.T) {
		hostnames := p.MustKubectl(
			t,
			"-n",
			w.Namespace,
			"get",
			"httproute",
			"resource-"+publicID+"-route",
			"-o",
			"jsonpath={.spec.hostnames[*]}",
		)
		if !strings.Contains(hostnames, publicHost) {
			t.Errorf("the public app's HTTPRoute serves %q, want %s", hostnames, publicHost)
		}
		requireObject(t, w, "networkpolicy", "resource-"+publicID+"-gateway", true)
		requireObject(t, w, "httproute", "resource-"+privateID+"-route", false)
		requireObject(t, w, "networkpolicy", "resource-"+privateID+"-gateway", false)
	})

	t.Run("only the public app is reachable from the gateway", func(t *testing.T) {
		if !gatewayReaches(t, w, publicID, "public") {
			t.Error("a gateway pod cannot reach the public app")
		}
		if gatewayReaches(t, w, privateID, "private") {
			t.Error("a gateway pod reaches the private app")
		}
		if !p.Reaches(
			t,
			platform.ProbePod{Namespace: w.Namespace, Name: "e2e-workspace-probe-private"},
			serviceURL(w, privateID),
		) {
			t.Error("a pod in the workspace cannot reach the private app")
		}
	})

	t.Run("status reports the URL", func(t *testing.T) {
		if out := w.MustLoco(
			t,
			"",
			"resource",
			"status",
			public,
		).Stdout; !strings.Contains(
			out,
			"https://"+publicHost,
		) {
			t.Errorf("loco resource status does not show the public URL:\n%s", out)
		}
		if out := w.MustLoco(
			t,
			"",
			"resource",
			"status",
			private,
		).Stdout; !regexp.MustCompile(`URL: *none`).
			MatchString(out) {
			t.Errorf("loco resource status shows a URL for the private app:\n%s", out)
		}
	})

	t.Run("losing the domain removes the route", func(t *testing.T) {
		domains := resourceDomains(t, w, public)
		if len(domains) == 0 {
			t.Fatal("the public app has no domain")
		}
		if _, err := p.User.Domain.DeleteResourceDomain(
			t.Context(),
			&domainv1.DeleteResourceDomainRequest{DomainId: domains[0].GetId()},
		); err != nil {
			t.Fatal(err)
		}
		if left := resourceDomains(t, w, public); len(left) != 0 {
			t.Errorf("the resource keeps domains %v", left)
		}
		w.MustLoco(t, "", "resource", "scale", public, "--replicas", "2")
		platform.Eventually(t, routeTimeout, "the HTTPRoute to be removed", func(ctx context.Context) error {
			if found, err := p.Exists(
				ctx,
				"-n",
				w.Namespace,
				"httproute",
				"resource-"+publicID+"-route",
			); err != nil ||
				found {
				return fmt.Errorf("the HTTPRoute exists (%w)", err)
			}
			return nil
		})
		requireObject(t, w, "networkpolicy", "resource-"+publicID+"-gateway", false)
		w.WaitReady(t, public)
		if gatewayReaches(t, w, publicID, "lost") {
			t.Error("a gateway pod still reaches the app that lost its domain")
		}
	})

	t.Run("plan rejects a domain no cluster serves", func(t *testing.T) {
		host := unserved + ".unserved.test"
		dir := t.TempDir()
		platform.WriteLocoFile(
			t,
			dir,
			locofile.File{
				Partial:  unserved,
				Services: map[string]locofile.Service{unserved: platform.ImageService(p.PublicImage, host)},
			},
		)
		result := w.Loco(t.Context(), dir, nil, "infra", "plan", "--json")
		if result.Err == nil {
			t.Fatalf("loco infra plan accepted a domain no cluster serves:\n%s", result.Output())
		}
		var plan struct {
			Errors []struct {
				Message string `json:"message"`
			} `json:"errors"`
		}
		if err := json.Unmarshal([]byte(result.Stdout), &plan); err != nil {
			t.Fatalf("decode the plan: %v\n%s", err, result.Output())
		}
		want := "domain is not under a platform domain any cluster serves: " + host
		if !slices.ContainsFunc(plan.Errors, func(e struct {
			Message string `json:"message"`
		}) bool {
			return strings.Contains(e.Message, want)
		}) {
			t.Errorf("the plan errors %+v do not name the unserved domain", plan.Errors)
		}
		if _, err := w.Resource(t.Context(), unserved); !platform.IsNotFound(err) {
			t.Errorf("the plan created a resource for the unserved domain (%v)", err)
		}
	})
}

func resourceDomains(t *testing.T, w *platform.Workspace, name string) []*domainv1.ResourceDomain {
	t.Helper()
	resource, err := w.Resource(t.Context(), name)
	if err != nil {
		t.Fatal(err)
	}
	return resource.GetDomains()
}

func requireObject(t *testing.T, w *platform.Workspace, kind, name string, want bool) {
	t.Helper()
	found, err := p.Exists(t.Context(), "-n", w.Namespace, kind, name)
	if err != nil {
		t.Fatal(err)
	}
	if found != want {
		t.Errorf("%s %s exists = %v, want %v", kind, name, found, want)
	}
}

func serviceURL(w *platform.Workspace, resourceID string) string {
	return "http://resource-" + resourceID + "." + w.Namespace + ".svc.cluster.local/"
}

// gatewayReaches probes the app from a pod carrying the Envoy proxy's labels in the gateway's
// namespace.
func gatewayReaches(t *testing.T, w *platform.Workspace, resourceID, probe string) bool {
	t.Helper()
	return p.Reaches(t, platform.ProbePod{
		Namespace: p.Namespace,
		Name:      "e2e-gateway-probe-" + probe + "-" + resourceID[len(resourceID)-6:],
		Labels: map[string]string{
			"gateway.envoyproxy.io/owning-gateway-name":      "eg",
			"gateway.envoyproxy.io/owning-gateway-namespace": p.Namespace,
		},
	}, serviceURL(w, resourceID))
}
