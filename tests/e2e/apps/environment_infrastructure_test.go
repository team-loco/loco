package apps

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"

	"github.com/team-loco/loco/internal/locofile"
	locolabels "github.com/team-loco/loco/k8sapi/labels"
	"github.com/team-loco/loco/tests/e2e/platform"
)

func TestEnvironmentInfrastructureDrift(t *testing.T) {
	t.Parallel()
	w := p.NewTenant(t)
	const name = "environment-repair"
	dir := t.TempDir()
	platform.WriteLocoFile(t, dir, locofile.File{Partial: name,
		Services: map[string]locofile.Service{name: platform.ImageService(p.PublicImage, w.Host(name))}})
	w.MustLoco(t, dir, "deploy", "--yes", "--wait")
	resourceID := w.ResourceID(t, name)
	const policyName = "default-deny"
	var policy networkingv1.NetworkPolicy
	p.MustGet(t, &policy, "-n", w.Namespace, "networkpolicy", policyName)
	original := policy.UID
	p.MustKubectl(t, "-n", w.Namespace, "delete", "networkpolicy", policyName)
	platform.Eventually(t, time.Minute, "shared environment policy restored", func(ctx context.Context) error {
		if err := p.Get(ctx, &policy, "-n", w.Namespace, "networkpolicy", policyName); err != nil {
			return err
		}
		if policy.UID == original {
			return errors.New("policy has not been recreated")
		}
		if len(policy.Spec.Ingress) != 0 || len(policy.Spec.Egress) != 0 {
			return errors.New("restored policy does not deny traffic")
		}
		return nil
	})
	p.MustKubectl(t, "-n", w.Namespace, "label", "networkpolicy", policyName, locolabels.LabelManagedBy+"-")
	platform.Eventually(t, time.Minute, "environment policy metadata restored", func(ctx context.Context) error {
		if err := p.Get(ctx, &policy, "-n", w.Namespace, "networkpolicy", policyName); err != nil {
			return err
		}
		if policy.Labels[locolabels.LabelManagedBy] != locolabels.ControllerManagedBy {
			return errors.New("management label not restored")
		}
		return nil
	})
	accountName := "resource-" + resourceID
	var account corev1.ServiceAccount
	p.MustGet(t, &account, "-n", w.Namespace, "serviceaccount", accountName)
	accountUID := account.UID
	p.MustKubectl(t, "-n", w.Namespace, "delete", "serviceaccount", accountName)
	platform.Eventually(t, time.Minute, "workload service account restored", func(ctx context.Context) error {
		if err := p.Get(ctx, &account, "-n", w.Namespace, "serviceaccount", accountName); err != nil {
			return err
		}
		if account.UID == accountUID {
			return errors.New("service account has not been recreated")
		}
		return nil
	})
	probe := platform.ProbePod{Namespace: w.Namespace, Name: "environment-repair-probe"}
	p.DeleteOnCleanup(t, "-n", w.Namespace, "pod", probe.Name)
	if !p.Reaches(t, probe, fmt.Sprintf("http://%s.%s.svc.cluster.local/", name, w.Namespace)) {
		t.Fatal("service was unreachable after environment infrastructure repair")
	}
	t.Logf(
		"synthetic service %s: shared policy and account recreated with new UIDs; metadata restored; service reachable",
		resourceID,
	)
}
