# Build context is the repository root, not web/ — the UI imports generated
# TypeScript from gen/ts, and bun workspaces hoist node_modules to the root.
FROM --platform=$BUILDPLATFORM oven/bun:1.4.4-alpine@sha256:4cf82f5686b196504fe119db3fcd9185b191f82ece82c40680e597228ec6070a AS builder

WORKDIR /app

# renovate: datasource=repology depName=alpine_3_24/brotli versioning=loose
ARG BROTLI_VERSION=1.2.1-r0
RUN apk add --no-cache brotli=${BROTLI_VERSION}

# Workspace manifests first, so dependency install caches independently of source.
COPY package.json bun.lock ./
COPY web/package.json ./web/
COPY gen/ts/package.json ./gen/ts/
COPY docs/package.json ./docs/
RUN bun install --frozen-lockfile

COPY gen/ts ./gen/ts
COPY web ./web

ARG PUBLIC_API_URL
ARG PUBLIC_APP_ENV
ENV PUBLIC_API_URL=$PUBLIC_API_URL
ENV PUBLIC_APP_ENV=$PUBLIC_APP_ENV

ARG VERSION
WORKDIR /app/web
RUN bun run build
WORKDIR /app

RUN find web/dist -type f -size +1k \( \
    -name '*.js' -o \
    -name '*.css' -o \
    -name '*.html' -o \
    -name '*.svg' -o \
    -name '*.json' -o \
    -name '*.woff2' \
    \) -exec brotli -f -k -q 11 {} \;


FROM builder AS docs-builder
WORKDIR /app/docs
COPY docs ./
ARG DOCS_ENVIRONMENT=production
RUN bun run build "$DOCS_ENVIRONMENT"

FROM ghcr.io/static-web-server/static-web-server:2.44.1@sha256:94618e3d17802265a21926217e6f9e7832b311b6fbf5933684001739fafed06a

COPY --from=docs-builder /app/docs/.sws.toml /etc/sws.toml
COPY --from=builder /app/web/dist /public
COPY --from=docs-builder /app/docs/site /documentation
COPY --from=docs-builder /app/docs/site-mounted /public/docs
COPY --from=docs-builder /app/docs/site/skills.md /public/skills.md
COPY schemas/loco.v1.json /public/schemas/loco.v1.json

EXPOSE 8080

USER 65532:65532

CMD ["--config-file", "/etc/sws.toml"]
