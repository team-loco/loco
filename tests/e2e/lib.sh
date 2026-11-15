#!/usr/bin/env bash
# Shared helpers of the control-plane e2e script

set -euo pipefail

# pull_prebuilt <reference> <tag> pulls the reference's manifest for the Docker host's platform and tags it.
pull_prebuilt() {
    local reference=$1 tag=$2 arch digest
    arch=$(docker version --format '{{.Server.Arch}}')
    digest=$(docker buildx imagetools inspect --raw "$reference" |
        jq -r --arg arch "$arch" '.manifests[]? | select(.platform.os == "linux" and .platform.architecture == $arch) | .digest')
    if [ -n "$digest" ]; then
        reference="${reference%:*}@$digest"
    fi
    docker pull -q "$reference"
    docker tag "$reference" "$tag"
}

# check_api_restarts fails unless every restart of an API pod in namespace $1 with label
# selector $2 was the expected startup race: the API exits when it cannot verify the
# transit keys before OpenBao has created them.
check_api_restarts() {
    local namespace=$1 selector=$2 pod restarts previous
    for pod in $(kubectl -n "$namespace" get pods -l "$selector" -o jsonpath='{.items[*].metadata.name}'); do
        restarts=$(kubectl -n "$namespace" get pod "$pod" -o jsonpath='{.status.containerStatuses[0].restartCount}')
        if [[ $restarts == 0 ]]; then continue; fi
        # Capture before matching: under pipefail, grep -q on a pipe fails a match once kubectl gets SIGPIPE.
        previous=$(kubectl -n "$namespace" logs "$pod" --previous)
        if [[ $previous != *'loco api failed error="verify the transit keys: '* ]]; then
            printf 'API pod %s restarted %s times without logging why it could not verify the transit keys; last termination: %s\n' \
                "$pod" "$restarts" \
                "$(kubectl -n "$namespace" get pod "$pod" -o jsonpath='{.status.containerStatuses[0].lastState.terminated}')" >&2
            return 1
        fi
    done
}
