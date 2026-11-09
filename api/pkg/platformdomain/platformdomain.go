// Package platformdomain decides which platform domains the clusters serve and which of them a
// service hostname belongs to. Plan, Apply and the domain RPCs share these rules.
package platformdomain

import (
	"fmt"
	"slices"
	"strings"
)

// Cluster is a cluster and the platform domains it serves.
type Cluster struct {
	Name    string
	Region  string
	Domains []string
}

// Served returns the sorted, distinct platform domains the clusters serve.
func Served(clusters []Cluster) []string {
	var domains []string
	for _, cluster := range clusters {
		domains = append(domains, cluster.Domains...)
	}
	slices.Sort(domains)
	return slices.Compact(domains)
}

// Check matches domain against the platform domains clusters serve and requires every target
// cluster to serve the one it matches. It returns the platform domain and the label in front of it.
func Check(domain string, clusters, targets []Cluster) (string, string, error) {
	platform, label, err := Match(domain, Served(clusters))
	if err != nil {
		return "", "", err
	}
	var missing []string
	for _, cluster := range targets {
		if !slices.Contains(cluster.Domains, platform) {
			missing = append(missing, cluster.Name+" ("+cluster.Region+")")
		}
	}
	if len(missing) > 0 {
		return "", "", fmt.Errorf(
			"%w: %s is under %s, which %s does not serve",
			ErrDomainNotServed, domain, platform, strings.Join(missing, ", "),
		)
	}
	return platform, label, nil
}

// Match returns the platform domain a service domain belongs to and the label in
// front of it. When platform domains nest, the longest suffix wins, so with loco.test and
// stage.loco.test served, api.stage.loco.test is the label api under stage.loco.test. The
// domain must be exactly one label under that platform domain.
func Match(domain string, platformDomains []string) (string, string, error) {
	matched := ""
	for _, platform := range platformDomains {
		if strings.HasSuffix(domain, "."+platform) && len(platform) > len(matched) {
			matched = platform
		}
	}
	if matched == "" {
		return "", "", ErrCustomDomain.Detail("%s", domain)
	}
	label := strings.TrimSuffix(domain, "."+matched)
	if label == "" || strings.Contains(label, ".") {
		return "", "", ErrNotSingleLabel.Detail("%s under %s", domain, matched)
	}
	return matched, label, nil
}
