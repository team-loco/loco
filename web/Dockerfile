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


FROM alpine:3.22.5

RUN apk --no-cache upgrade \
    && apk add --no-cache nginx=1.28.3-r7 nginx-mod-http-brotli=1.28.3-r7 \
    && mkdir -p /run/nginx /usr/share/nginx/html \
    && chown -R nginx:nginx /run/nginx /var/lib/nginx /var/log/nginx

COPY web/nginx.conf /etc/nginx/nginx.conf
COPY --from=builder /app/web/dist /usr/share/nginx/html

EXPOSE 8080

USER 100:101

ENTRYPOINT []
CMD ["nginx", "-g", "daemon off;"]
