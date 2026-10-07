FROM busybox:1.38.0-musl AS shell

FROM ghcr.io/project-zot/zot:v2.1.22

COPY --from=shell /bin/busybox /bin/busybox
COPY --chmod=0755 registry/entrypoint.sh /entrypoint.sh
COPY registry/config.json /etc/zot/config.json.tmpl

ENTRYPOINT ["/entrypoint.sh"]
