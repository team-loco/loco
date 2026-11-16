package gitops

import "embed"

// Files contains the operator repository scaffold.
//
//go:embed README.md .sops.yaml releases clusters
var Files embed.FS
