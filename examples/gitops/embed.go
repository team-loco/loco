package gitops

import "embed"

// Files contains the operator repository scaffold.
//
//go:embed README.md releases clusters
var Files embed.FS
