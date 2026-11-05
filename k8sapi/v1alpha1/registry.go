package v1alpha1

const (
	LabelRegistryPull               = "loco.io/registry-pull"
	AnnotationRegistryPullIssuedAt  = "loco.io/registry-pull-issued-at"
	AnnotationRegistryPullExpiresAt = "loco.io/registry-pull-expires-at"

	RegistryAuthStatic    = "static"
	RegistryAuthToken     = "token"
	RegistryPullUsername  = "loco-pull"
	RegistryBuildUsername = "loco-build"

	registryPullSecretPrefix  = "loco-registry-pull-" //nolint:gosec
	buildRegistrySecretPrefix = "build-"
	buildRegistrySecretSuffix = "-registry"
)

func RegistryPullSecretName(workspaceID string) string {
	return registryPullSecretPrefix + workspaceID
}

func BuildRegistrySecretName(buildID string) string {
	return buildRegistrySecretPrefix + buildID + buildRegistrySecretSuffix
}
