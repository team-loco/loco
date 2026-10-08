FROM busybox:1.38.0@sha256:fd7dc98638c8e305f4dc34e979f1c0fdfdcaeb0fbf8fcff77ae834b6da3d7e6e
SHELL ["/bin/ash", "-o", "pipefail", "-c"]
RUN for p in /credentials /credentials/push /credentials/pull /var/run/secrets/kubernetes.io/serviceaccount /run/secrets/kubernetes.io; do \
        if [ -e "$p" ]; then echo "PROBE-LEAK path $p"; fi; \
    done; \
    for p in /proc/[0-9]*; do \
        if tr '\0' '\n' < "$p/environ" 2>/dev/null | grep -E '^DOCKER_CONFIG=|^SOURCE_URL=' > /dev/null; then echo "PROBE-LEAK environment of $p"; fi; \
    done; \
    if grep -r -q -s -E '"auths"' / --exclude-dir=proc --exclude-dir=sys; then echo "PROBE-LEAK docker config file"; fi; \
    api=$(wget -q -T 5 -O - http://kubernetes.default.svc:443/ 2>&1 || true); \
    case "$api" in *400*|*"Bad Request"*) echo "PROBE-LEAK kubernetes api reachable" ;; esac; \
    echo "kubernetes api probe: $api"; \
    echo "PROBE-DONE"
