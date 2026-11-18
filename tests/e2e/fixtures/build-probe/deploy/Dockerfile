FROM busybox:1.38.0@sha256:a46257e06c82ad85242d0d38077d0a52d9896389ca60a1a59d3463db586e5fa4
SHELL ["/bin/ash", "-o", "pipefail", "-c"]
RUN api=$(wget -q -T 5 -O - http://kubernetes.default.svc:443/ 2>&1 || true); \
    echo "kubernetes api probe: $api"; \
    case "$api" in \
        *400*|*"Bad Request"*) echo "PROBE-LEAK kubernetes api reachable" ;; \
        *"timed out"*) ;; \
        *) echo "PROBE-ERROR kubernetes api probe" ;; \
    esac; \
    for p in /credentials /credentials/push /credentials/pull /var/run/secrets/kubernetes.io/serviceaccount /run/secrets/kubernetes.io; do \
        if [ -e "$p" ]; then echo "PROBE-LEAK path $p"; fi; \
    done; \
    for p in /proc/[0-9]*; do \
        if environ=$(tr '\0' '\n' < "$p/environ" 2>/dev/null) && echo "$environ" | grep -q -E '^DOCKER_CONFIG=|^SOURCE_URL='; then \
            echo "PROBE-LEAK environment of $p"; \
        fi; \
    done; \
    files=$(find / \( -path /proc -o -path /sys -o -path /dev \) -prune -o -type f -print) || echo "PROBE-ERROR file list"; \
    echo "docker config probe: $(echo "$files" | wc -l) files"; \
    echo "$files" | while read -r f; do \
        grep -q -F '"auths"' "$f"; \
        case $? in \
            0) echo "PROBE-LEAK docker config file $f" ;; \
            1) ;; \
            *) echo "PROBE-ERROR reading $f" ;; \
        esac; \
    done; \
    echo "PROBE-DONE"
