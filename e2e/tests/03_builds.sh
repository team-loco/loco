#!/usr/bin/env bash

if [ "${E2E_BUILDS_ENABLED:-true}" = false ]; then
    E2E_SKIP_REASON="builds are disabled"
fi

builds_ns="$E2E_BUILD_NAMESPACE"
builds_ctx="kind-${E2E_KIND_CLUSTER}"
builds_dir="$E2E_BUILD_WORK_DIR"
builds_run_ns="e2e-build-run"
builds_app_repo="${E2E_REGISTRY_HOST}/e2e/build-app"
builds_probe_repo="${E2E_REGISTRY_HOST}/e2e/build-probe"
builds_digest_pattern='^sha256:[a-f0-9]{64}$'

bk() {
    kubectl --context "$builds_ctx" "$@"
}

s3() {
    docker run --rm --network kind \
        -e AWS_ACCESS_KEY_ID="$LOCO_SOURCE_BUCKET_ACCESS_KEY_ID" \
        -e AWS_SECRET_ACCESS_KEY="$LOCO_SOURCE_BUCKET_SECRET_ACCESS_KEY" \
        -e AWS_DEFAULT_REGION="$LOCO_SOURCE_BUCKET_REGION" \
        -e AWS_CONFIG_FILE=/work/aws-config \
        -v "$builds_dir:/work" \
        "$E2E_AWS_CLI_IMAGE" --endpoint-url "$E2E_S3_ENDPOINT" "$@"
}

upload_source() {
    local name=$1 fixture=$2
    mkdir -p "$builds_dir"
    printf '[default]\ns3 =\n  addressing_style = path\n' >"$builds_dir/aws-config"
    COPYFILE_DISABLE=1 tar -czf "$builds_dir/$name.tar.gz" -C "$E2E_ROOT_DIR/e2e/fixtures/$fixture" .
    s3 s3 cp --only-show-errors "/work/$name.tar.gz" "s3://$LOCO_SOURCE_BUCKET/sources/$name.tar.gz" >&2
    s3 s3 presign "s3://$LOCO_SOURCE_BUCKET/sources/$name.tar.gz" --expires-in 3600
}

apply_build() {
    local name=$1 url=$2 dockerfile=$3 repository=$4 cache_ref=${5:-}
    local cache_line=""
    if [ -n "$cache_ref" ]; then
        cache_line="  cacheRef: \"$cache_ref\""
    fi
    bk apply -f - >/dev/null <<YAML
apiVersion: infra.loco.io/v1alpha1
kind: Build
metadata:
  name: $name
  namespace: $builds_ns
spec:
  buildId: $name
  workspaceId: e2e-workspace
  resourceId: e2e-resource
  sourceURL: "$url"
  dockerfilePath: $dockerfile
  imageRepository: $repository
$cache_line
YAML
}

build_field() {
    bk -n "$builds_ns" get build "$1" -o jsonpath="{.status.$2}" 2>/dev/null
}

wait_build() {
    local name=$1 max=${2:-900} elapsed=0 phase=""
    log_info "Waiting for build ${name} (up to ${max}s)..."
    while [ "$elapsed" -lt "$max" ]; do
        if ! bk -n "$builds_ns" get build "$name" >/dev/null 2>&1; then
            log_error "Build ${name} does not exist"
            return 1
        fi
        phase=$(build_field "$name" phase)
        case "$phase" in
            Succeeded) break ;;
            Failed) break ;;
            Canceled) break ;;
        esac
        sleep 3
        elapsed=$((elapsed + 3))
    done
    log_info "Build ${name}: ${phase:-no phase} after ${elapsed}s ($(build_field "$name" message))"
    if [ "$phase" != Succeeded ]; then
        dump_build "$name"
    fi
}

build_seconds() {
    local started finished
    started=$(build_field "$1" startedAt)
    finished=$(build_field "$1" finishedAt)
    if [ -z "$started" ] || [ -z "$finished" ]; then
        echo "?"
        return
    fi
    echo $(($(date_seconds "$finished") - $(date_seconds "$started")))
}

date_seconds() {
    date -u -d "$1" +%s 2>/dev/null || date -u -j -f "%Y-%m-%dT%H:%M:%SZ" "$1" +%s
}

build_logs() {
    bk -n "$builds_ns" logs "job/$1" -c "$2" 2>&1
}

save_build_output() {
    bk -n "$builds_ns" logs "job/$1" -c build >"$builds_dir/$1.build.log"
}

build_output_has() {
    grep -q -E "^#[0-9]+ [0-9.]+ $2" "$builds_dir/$1.build.log"
}

dump_build() {
    local name=$1
    log_error "Diagnostics for build ${name}:"
    {
        echo "--- build"
        bk -n "$builds_ns" get build "$name" -o yaml
        echo "--- job"
        bk -n "$builds_ns" describe job "$name"
        echo "--- pods"
        bk -n "$builds_ns" describe pods -l "loco.io/build-id=$name"
        for container in fetch build push; do
            echo "--- logs: $container"
            build_logs "$name" "$container" | tail -100
        done
        echo "--- events"
        bk -n "$builds_ns" get events --sort-by=.lastTimestamp | tail -30
        echo "--- controller"
        bk -n "$E2E_LOCO_NAMESPACE" logs deployment/loco-build-controller --tail=100
    } 2>&1 | sed 's/^/    /'
}

digest_valid() {
    [[ $1 =~ $builds_digest_pattern ]]
}

test_b01_build_app() {
    local url
    url=$(upload_source e2e-app-1 build-app)
    assert "Presigned GET URL points at the bucket" test -n "$url"
    apply_build e2e-app-1 "$url" deploy/Dockerfile "$builds_app_repo"
    wait_build e2e-app-1

    assert "First build reached Succeeded" test "$(build_field e2e-app-1 phase)" = Succeeded
    local image_digest cache_digest
    image_digest=$(build_field e2e-app-1 imageDigest)
    cache_digest=$(build_field e2e-app-1 cacheDigest)
    assert "First build reported an image digest (${image_digest})" digest_valid "$image_digest"
    assert "First build reported a cache digest (${cache_digest})" digest_valid "$cache_digest"
    echo "$image_digest" >"$builds_dir/app.image"
    echo "$cache_digest" >"$builds_dir/app.cache"
    log_info "First build took $(build_seconds e2e-app-1)s"
}

container_mounts() {
    local field=$1 name=$2
    bk -n "$builds_ns" get job e2e-app-1 -o \
        jsonpath="{.spec.template.spec.${field}[?(@.name==\"${name}\")].volumeMounts[*].name}"
}

test_b02_push_secret_only_in_push() {
    assert_contains "push step mounts the push credentials" push-credentials \
        container_mounts containers push
    assert_fails "build step mounts no credentials" \
        bash -c "kubectl --context $builds_ctx -n $builds_ns get job e2e-app-1 -o \
            jsonpath='{.spec.template.spec.initContainers[?(@.name==\"build\")].volumeMounts[*].name}' | grep -q credentials"
    assert_fails "fetch step does not mount the push credentials" \
        bash -c "kubectl --context $builds_ctx -n $builds_ns get job e2e-app-1 -o \
            jsonpath='{.spec.template.spec.initContainers[?(@.name==\"fetch\")].volumeMounts[*].name}' | grep -q push-credentials"
    assert_contains "build pod gets no service account token" false \
        bk -n "$builds_ns" get job e2e-app-1 -o jsonpath='{.spec.template.spec.automountServiceAccountToken}'
}

test_b03_image_pullable_with_nodes_account() {
    local digest
    digest=$(cat "$builds_dir/app.image" 2>/dev/null)
    assert "nodes account pulls the image by digest" \
        test "$(registry_manifest_status "$E2E_REGISTRY_NODES_USER" e2e/build-app "$digest")" = 200
    assert "anonymous pull is refused" \
        test "$(registry_manifest_status "" e2e/build-app "$digest")" = 401
}

run_deployment_ready() {
    bk -n "$builds_run_ns" rollout status deployment/build-app --timeout=5s
}

test_b04_image_runs() {
    local digest config
    digest=$(cat "$builds_dir/app.image" 2>/dev/null)
    config=$(bk -n "$E2E_LOCO_NAMESPACE" get secret loco-registry -o jsonpath='{.data.\.dockerconfigjson}' | base64 -d)
    bk create namespace "$builds_run_ns" --dry-run=client -o yaml | bk apply -f - >/dev/null
    bk -n "$builds_run_ns" create secret generic registry-pull \
        --type=kubernetes.io/dockerconfigjson \
        --from-literal=.dockerconfigjson="$config" \
        --dry-run=client -o yaml | bk apply -f - >/dev/null
    bk apply -f - >/dev/null <<YAML
apiVersion: apps/v1
kind: Deployment
metadata:
  name: build-app
  namespace: $builds_run_ns
spec:
  replicas: 1
  selector:
    matchLabels:
      app: build-app
  template:
    metadata:
      labels:
        app: build-app
    spec:
      imagePullSecrets:
        - name: registry-pull
      containers:
        - name: app
          image: ${builds_app_repo}@${digest}
          ports:
            - containerPort: 8080
          readinessProbe:
            httpGet:
              path: /healthz
              port: 8080
            periodSeconds: 2
YAML
    wait_for "built image to run and pass its readiness probe" 180 run_deployment_ready
    if ! assert "Deployment of the built image is Ready" run_deployment_ready; then
        bk -n "$builds_run_ns" describe pods | sed 's/^/    /'
    fi
}

test_b05_rebuild_restores_cache() {
    local cache_digest url
    cache_digest=$(cat "$builds_dir/app.cache" 2>/dev/null)
    url=$(upload_source e2e-app-2 build-app)
    apply_build e2e-app-2 "$url" deploy/Dockerfile "$builds_app_repo" "${builds_app_repo}@${cache_digest}"
    wait_build e2e-app-2

    assert "Cached build reached Succeeded" test "$(build_field e2e-app-2 phase)" = Succeeded
    assert_contains "fetch step restored the cache by digest" "cache restored" build_logs e2e-app-2 fetch
    assert_contains "build step reused cached layers" CACHED build_logs e2e-app-2 build
    assert "Cached build pushed a new image digest" digest_valid "$(build_field e2e-app-2 imageDigest)"
    log_info "Cached build took $(build_seconds e2e-app-2)s (first build: $(build_seconds e2e-app-1)s)"
}

test_b06_untrusted_build_finds_no_credentials() {
    local url
    url=$(upload_source e2e-probe build-probe)
    apply_build e2e-probe "$url" Dockerfile "$builds_probe_repo"
    wait_build e2e-probe

    assert "Probe build reached Succeeded" test "$(build_field e2e-probe phase)" = Succeeded
    if ! assert "Read the probe build's output" save_build_output e2e-probe; then
        return 1
    fi
    assert "Probe ran to completion inside the build" build_output_has e2e-probe PROBE-DONE
    log_info "$(grep -m 1 -E '^#[0-9]+ [0-9.]+ kubernetes api probe:' "$builds_dir/e2e-probe.build.log")"
    if ! assert_fails "Probe found no credentials, tokens or Kubernetes API access" \
        build_output_has e2e-probe PROBE-LEAK; then
        grep -E 'PROBE-' "$builds_dir/e2e-probe.build.log" | sed 's/^/    /'
    fi
}
