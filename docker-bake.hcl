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

target "service" {
  name = svc.name
  matrix = {
    svc = [
      { name = "controller", dir = "controller" },
      { name = "api", dir = "api" },
      { name = "agent", dir = "agent" },
      { name = "builder", dir = "builder" },
      { name = "obs-proxy", dir = "observability-proxy" },
    ]
  }
  inherits   = ["_platform"]
  dockerfile = "${svc.dir}/Dockerfile"
  tags       = ["${REGISTRY}-${svc.name}:${TAG}"]
  cache-from = [cache(svc.name)]
  cache-to   = WRITE_CACHE ? ["${cache(svc.name)},mode=max"] : []
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
