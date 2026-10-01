# Build context is the repository root, not web/ — the UI imports generated
# TypeScript from gen/ts, and bun workspaces hoist node_modules to the root.
FROM oven/bun:1.4.0-alpine AS builder

ARG VITE_API_URL
ARG VITE_APP_ENV
ENV VITE_API_URL=$VITE_API_URL
ENV VITE_APP_ENV=$VITE_APP_ENV

WORKDIR /app

# Workspace manifests first, so dependency install caches independently of source.
COPY package.json bun.lock ./
COPY web/package.json ./web/
COPY gen/ts/package.json ./gen/ts/
RUN bun install --frozen-lockfile

COPY gen/ts ./gen/ts
COPY web ./web

RUN apk add --no-cache brotli=1.1.0-r2

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


FROM ghcr.io/static-web-server/static-web-server:2.44.0

COPY web/sws.toml /etc/sws.toml
COPY --from=builder /app/web/dist /public

EXPOSE 8080

USER 65532:65532

CMD ["--config-file", "/etc/sws.toml"]
