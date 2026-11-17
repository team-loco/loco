package workloadplatform

import (
	"fmt"
	"strings"
)

// Platform is the installation's application and source-build target.
type Platform string

const (
	AMD64 Platform = "linux/amd64"
	ARM64 Platform = "linux/arm64"
)

func Parse(value string) (Platform, error) {
	p := Platform(value)
	if p != AMD64 && p != ARM64 {
		return "", fmt.Errorf("unsupported workload platform %q: use linux/amd64 or linux/arm64", value)
	}
	return p, nil
}

func (p Platform) Architecture() string {
	return strings.TrimPrefix(string(p), "linux/")
}
