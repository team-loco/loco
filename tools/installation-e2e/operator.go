package main

import (
	"context"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
)

func (f *fixture) configureClient(ctx context.Context, previous, current, image string) error {
	allowances := []map[string]any{}
	for _, service := range []struct {
		name string
		port int
	}{{fixtureRegistry, 5000}, {sourceService, 8080}} {
		for _, query := range []struct{ resource, selector, path string }{
			{"service", service.name, "{.spec.clusterIP}"},
			{"pod", "-l=fixture=" + service.name, "{.items[0].status.podIP}"},
		} {
			output, inspectErr := command(
				ctx,
				f.root,
				nil,
				"kubectl",
				"-n",
				fixtureNamespace,
				"get",
				query.resource,
				query.selector,
				"-o",
				"jsonpath="+query.path,
			)
			if inspectErr != nil {
				return inspectErr
			}
			address, parseErr := netip.ParseAddr(strings.TrimSpace(string(output)))
			if parseErr != nil {
				return parseErr
			}
			allowances = append(
				allowances,
				map[string]any{"cidr": netip.PrefixFrom(address, address.BitLen()).String(), "port": service.port},
			)
		}
	}
	if err := f.installFlux(ctx, allowances, previous); err != nil {
		return err
	}
	probe, err := os.ReadFile(filepath.Join(f.root, "tests", "e2e", "fixtures", "public-image", "Dockerfile"))
	if err != nil {
		return err
	}
	probeImage := strings.Fields(strings.TrimSpace(string(probe)))[1]
	buildNamespace, err := f.pin(ctx, "charts/loco-worker/values.yaml", ".builds.namespace")
	if err != nil {
		return err
	}
	if _, deleteErr := command(
		ctx,
		f.root,
		nil,
		"kubectl",
		"-n",
		fixtureNamespace,
		"delete",
		"pod",
		"operator",
		"--ignore-not-found",
		"--wait=true",
	); deleteErr != nil {
		return deleteErr
	}
	parameters := map[string]any{
		"Previous":       previous,
		"Current":        current,
		"Image":          image,
		"ProbeImage":     probeImage,
		"BuildNamespace": buildNamespace,
	}
	return f.renderFixture(ctx, "operator.yaml", parameters)
}
