package controller

import (
	"context"
	"fmt"
	"log/slog"
	"net/netip"
	"slices"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	locov1alpha1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

type egressExclusionSet struct {
	ipv4 []string
	ipv6 []string
}

func nodeAddressRanges(node *corev1.Node) []string {
	cidrs := node.Spec.PodCIDRs
	if len(cidrs) == 0 && node.Spec.PodCIDR != "" {
		cidrs = []string{node.Spec.PodCIDR}
	}
	ranges := slices.Clone(cidrs)
	for _, address := range node.Status.Addresses {
		if address.Type != corev1.NodeInternalIP {
			continue
		}
		addr, err := netip.ParseAddr(address.Address)
		if err != nil {
			continue
		}
		unmapped := addr.Unmap()
		prefix := netip.PrefixFrom(unmapped, unmapped.BitLen())
		ranges = append(ranges, prefix.String())
	}
	slices.Sort(ranges)
	return ranges
}

func clusterAddressRanges(ctx context.Context, reader client.Reader) ([]string, error) {
	var nodes corev1.NodeList
	if err := reader.List(ctx, &nodes); err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}
	var ranges []string
	for i := range nodes.Items {
		nodeRanges := nodeAddressRanges(&nodes.Items[i])
		ranges = append(ranges, nodeRanges...)
	}

	var serviceCIDRs networkingv1.ServiceCIDRList
	err := reader.List(ctx, &serviceCIDRs)
	if meta.IsNoMatchError(err) || apierrors.IsNotFound(err) {
		slog.DebugContext(ctx, "ServiceCIDR API not served, skipping service ranges", "error", err)
		return ranges, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list service cidrs: %w", err)
	}
	for i := range serviceCIDRs.Items {
		ranges = append(ranges, serviceCIDRs.Items[i].Spec.CIDRs...)
	}
	return ranges, nil
}

func parsePrefixes(cidrs []string) []netip.Prefix {
	prefixes := make([]netip.Prefix, 0, len(cidrs))
	for _, cidr := range cidrs {
		prefix, err := netip.ParsePrefix(cidr)
		if err != nil {
			continue
		}
		masked := prefix.Masked()
		addr := masked.Addr()
		if addr.Is4In6() {
			unmappedAddr := addr.Unmap()
			ipv4Bits := max(masked.Bits()-96, 0)
			masked = netip.PrefixFrom(unmappedAddr, ipv4Bits)
		}
		prefixes = append(prefixes, masked)
	}
	return prefixes
}

func coveredBy(prefix netip.Prefix, covering []netip.Prefix) bool {
	for _, outer := range covering {
		if outer.Bits() <= prefix.Bits() && outer.Contains(prefix.Addr()) {
			return true
		}
	}
	return false
}

func comparePrefixes(a, b netip.Prefix) int {
	if c := a.Addr().Compare(b.Addr()); c != 0 {
		return c
	}
	return a.Bits() - b.Bits()
}

func prefixStrings(prefixes []netip.Prefix) []string {
	slices.SortFunc(prefixes, comparePrefixes)
	prefixes = slices.Compact(prefixes)
	out := make([]string, 0, len(prefixes))
	for _, prefix := range prefixes {
		out = append(out, prefix.String())
	}
	return out
}

func discoverEgressExclusions(ctx context.Context, reader client.Reader) (egressExclusionSet, error) {
	discovered, err := clusterAddressRanges(ctx, reader)
	if err != nil {
		return egressExclusionSet{}, fmt.Errorf("discover cluster address ranges: %w", err)
	}
	return egressExclusions(discovered), nil
}

func egressExclusions(discovered []string) egressExclusionSet {
	reserved4 := parsePrefixes(reservedIPv4)
	reserved6 := parsePrefixes(reservedIPv6)
	ipv4 := slices.Clone(reserved4)
	ipv6 := slices.Clone(reserved6)
	for _, prefix := range parsePrefixes(discovered) {
		if prefix.Addr().Is4() {
			if !coveredBy(prefix, reserved4) {
				ipv4 = append(ipv4, prefix)
			}
			continue
		}
		if !coveredBy(prefix, reserved6) {
			ipv6 = append(ipv6, prefix)
		}
	}
	ipv4Strings := prefixStrings(ipv4)
	ipv6Strings := prefixStrings(ipv6)
	return egressExclusionSet{ipv4: ipv4Strings, ipv6: ipv6Strings}
}

func nodeRangesChanged() predicate.Funcs {
	return predicate.Funcs{
		CreateFunc: func(event.CreateEvent) bool { return true },
		DeleteFunc: func(event.DeleteEvent) bool { return true },
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldNode, oldOK := e.ObjectOld.(*corev1.Node)
			newNode, newOK := e.ObjectNew.(*corev1.Node)
			if !oldOK || !newOK {
				return false
			}
			oldRanges := nodeAddressRanges(oldNode)
			newRanges := nodeAddressRanges(newNode)
			return !slices.Equal(oldRanges, newRanges)
		},
		GenericFunc: func(event.GenericEvent) bool { return false },
	}
}

func (r *LocoResourceReconciler) applicationPerWorkspace(ctx context.Context, _ client.Object) []reconcile.Request {
	var apps locov1alpha1.ApplicationList
	if err := r.List(ctx, &apps); err != nil {
		slog.WarnContext(ctx, "failed to list applications for a workspace-wide change", "error", err)
		return nil
	}
	seen := make(map[string]bool, len(apps.Items))
	var requests []reconcile.Request
	for i := range apps.Items {
		app := &apps.Items[i]
		if seen[app.Spec.WorkspaceID] || !app.DeletionTimestamp.IsZero() {
			continue
		}
		if err := app.Spec.Validate(); err != nil {
			continue
		}
		seen[app.Spec.WorkspaceID] = true
		key := client.ObjectKeyFromObject(app)
		requests = append(requests, reconcile.Request{NamespacedName: key})
	}
	return requests
}
