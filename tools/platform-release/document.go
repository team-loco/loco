package main

import "fmt"

// fields accesses maps constructed by the bundle generator, whose shapes are invariant.
func fields(item object, keys ...string) object {
	for _, key := range keys {
		nested, ok := item[key].(map[string]any)
		if !ok {
			panic(fmt.Sprintf("invalid generated document field %s", key))
		}
		item = nested
	}
	return item
}

func documentName(item object) string {
	name, ok := fields(item, fieldMetadata)[fieldName].(string)
	if !ok {
		panic("generated document has no name")
	}
	return name
}

func documentKind(item object) string {
	kind, ok := item[fieldKind].(string)
	if !ok {
		panic("generated document has no kind")
	}
	return kind
}

const (
	acceptHeader      = "Accept"
	kindKustomization = "Kustomization"
	kindOCIRepository = "OCIRepository"
	imageAgent        = "agent"
	archAMD64         = "amd64"
	fieldAPIVersion   = "apiVersion"
	fieldArchitecture = "architecture"
	archARM64         = "arm64"
	artifactsName     = "artifacts"
	fieldAuth         = "auth"
	buildOperation    = "build"
	buildsName        = "builds"
	certManagerName   = "cert-manager"
	controlPlaneName  = "control-plane"
	fieldCoreCommit   = "coreCommit"
	crdsName          = "crds"
	fieldDigest       = "digest"
	dockerCommand     = "docker"
	fieldEnabled      = "enabled"
	fluxNamespace     = "flux-system"
	gatewayName       = "gateway"
	fieldImage        = "image"
	fieldInterval     = "interval"
	fieldKind         = "kind"
	fieldMediaType    = "mediaType"
	fieldMetadata     = "metadata"
	fieldName         = "name"
	reconcilerAccount = "platform-reconciler"
	pushOperation     = "push"
)

const (
	imageAPI       = "api"
	bundleCommand  = "bundle"
	infraLayer     = "infra"
	linuxOS        = "linux"
	tagOperation   = "tag"
	fieldVersion   = "version"
	fieldNamespace = "namespace"
)
