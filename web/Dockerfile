# Build context is the repository root, not web/ — the UI imports generated
# TypeScript from gen/ts, and bun workspaces hoist node_modules to the root.
FROM oven/bun:1.4.2-alpine AS builder

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


FROM ghcr.io/astral-sh/uv:0.9.28 AS docs-uv

FROM python:3.13.16-slim AS docs-builder
COPY --from=docs-uv /uv /usr/local/bin/uv
WORKDIR /app/docs
COPY docs/pyproject.toml docs/uv.lock ./
RUN uv sync --frozen --no-dev
COPY docs/zensical.toml ./
COPY docs/scripts ./scripts
COPY docs/content ./content
COPY docs/overrides ./overrides
COPY web/public/favicon.svg /app/web/public/favicon.svg
COPY web/src/components/design/logo-strokes.ts /app/web/src/components/design/logo-strokes.ts
COPY web/sws.toml /app/web/sws.toml
ARG DOCS_ENVIRONMENT=production
RUN uv run --frozen --no-dev python scripts/build.py build "$DOCS_ENVIRONMENT" \
    && uv run --frozen --no-dev python scripts/package.py

FROM ghcr.io/static-web-server/static-web-server:2.44.0

COPY --from=docs-builder /app/docs/.sws.toml /etc/sws.toml
COPY --from=builder /app/web/dist /public
COPY --from=docs-builder /app/docs/site /public/docs

EXPOSE 8080

USER 65532:65532

CMD ["--config-file", "/etc/sws.toml"]
