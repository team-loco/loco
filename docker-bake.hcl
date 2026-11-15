# Every image CI builds for a commit, tagged REGISTRY-<target>:TAG, with each
# image's registry build cache at REGISTRY-<target>:buildcache.
# `docker buildx bake --load <target>` builds one locally.

variable "REGISTRY" {
  default = "ghcr.io/team-loco/loco"
}

variable "TAG" {
  default = "dev"
}

variable "VERSION" {
  default = ""
}

# Comma-separated; empty builds for the local platform.
variable "PLATFORMS" {
  default = ""
}

# Set to true to write the build cache, which CI does on main.
variable "WRITE_CACHE" {
  default = false
}

# The operator fixture's tool versions, from mise.toml.
variable "HELM_VERSION" {
  default = ""
}

variable "KUBECTL_VERSION" {
  default = ""
}

function "cache" {
  params = [name]
  result = "type=registry,ref=${REGISTRY}-${name}:buildcache"
}

group "default" {
  targets = ["controller", "api", "agent", "builder", "obs-proxy", "ui", "api-failover", "installation-operator"]
}

# The images build.yml publishes from main.
group "release" {
  targets = ["default", "ui-staging"]
}

# Filled with the OCI labels by docker/metadata-action in CI.
target "docker-metadata-action" {}

target "_image" {
  inherits = ["docker-metadata-action"]
  context  = "."
  args = {
    VERSION = VERSION
  }
}

target "_platform" {
  inherits  = ["_image"]
  platforms = PLATFORMS == "" ? [] : split(",", PLATFORMS)
  attest    = ["type=provenance,mode=max"]
}

target "controller" {
  inherits   = ["_platform"]
  dockerfile = "controller/Dockerfile"
  tags       = ["${REGISTRY}-controller:${TAG}"]
  cache-from = [cache("controller")]
  cache-to   = WRITE_CACHE ? ["${cache("controller")},mode=max"] : []
}

target "api" {
  inherits   = ["_platform"]
  dockerfile = "api/Dockerfile"
  tags       = ["${REGISTRY}-api:${TAG}"]
  cache-from = [cache("api")]
  cache-to   = WRITE_CACHE ? ["${cache("api")},mode=max"] : []
}

target "agent" {
  inherits   = ["_platform"]
  dockerfile = "agent/Dockerfile"
  tags       = ["${REGISTRY}-agent:${TAG}"]
  cache-from = [cache("agent")]
  cache-to   = WRITE_CACHE ? ["${cache("agent")},mode=max"] : []
}

target "builder" {
  inherits   = ["_platform"]
  dockerfile = "builder/Dockerfile"
  tags       = ["${REGISTRY}-builder:${TAG}"]
  cache-from = [cache("builder")]
  cache-to   = WRITE_CACHE ? ["${cache("builder")},mode=max"] : []
}

target "obs-proxy" {
  inherits   = ["_platform"]
  dockerfile = "observability-proxy/Dockerfile"
  tags       = ["${REGISTRY}-obs-proxy:${TAG}"]
  cache-from = [cache("obs-proxy")]
  cache-to   = WRITE_CACHE ? ["${cache("obs-proxy")},mode=max"] : []
}

target "ui" {
  inherits   = ["_platform"]
  dockerfile = "web/Dockerfile"
  tags       = ["${REGISTRY}-ui:${TAG}"]
  args = {
    DOCS_ENVIRONMENT = "production"
    PUBLIC_API_URL   = "https://api.loco.build"
    PUBLIC_APP_ENV   = "PRODUCTION"
  }
  cache-from = [cache("ui")]
  cache-to   = WRITE_CACHE ? ["${cache("ui")},mode=max"] : []
}

target "ui-staging" {
  inherits   = ["_platform"]
  dockerfile = "web/Dockerfile"
  tags       = ["${REGISTRY}-ui:${TAG}-staging"]
  args = {
    VERSION          = "${VERSION}-staging"
    DOCS_ENVIRONMENT = "staging"
    PUBLIC_API_URL   = "https://api.staging.loco.build"
    PUBLIC_APP_ENV   = "STAGING"
  }
  cache-from = [cache("ui")]
}

target "api-failover" {
  inherits   = ["_image"]
  dockerfile = "tests/e2e/fixtures/api-failover/Dockerfile"
  tags       = ["${REGISTRY}-api-failover:${TAG}"]
  cache-from = [cache("api-failover")]
  cache-to   = WRITE_CACHE ? ["${cache("api-failover")},mode=max"] : []
}

target "installation-operator" {
  inherits   = ["_image"]
  dockerfile = "tests/e2e/fixtures/installation/operator.Dockerfile"
  tags       = ["${REGISTRY}-installation-operator:${TAG}"]
  args = {
    HELM_VERSION    = HELM_VERSION
    KUBECTL_VERSION = KUBECTL_VERSION
  }
  cache-from = [cache("installation-operator")]
  cache-to   = WRITE_CACHE ? ["${cache("installation-operator")},mode=max"] : []
}
