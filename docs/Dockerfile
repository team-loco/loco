FROM ghcr.io/astral-sh/uv:0.9.28 AS uv

FROM python:3.13.16-slim AS build
COPY --from=uv /uv /usr/local/bin/uv
WORKDIR /docs
COPY docs/pyproject.toml docs/uv.lock ./
RUN uv sync --frozen --no-dev
COPY docs/zensical.toml ./
COPY docs/scripts ./scripts
COPY docs/content ./content
COPY docs/overrides ./overrides
ARG DOCS_ENVIRONMENT=production
RUN uv run --frozen --no-dev python scripts/build.py build "$DOCS_ENVIRONMENT"

FROM caddy:2.11.7-alpine
COPY docs/Caddyfile /etc/caddy/Caddyfile
COPY --from=build /docs/site /srv
ENV PORT=8080 \
    XDG_CONFIG_HOME=/tmp/caddy/config \
    XDG_DATA_HOME=/tmp/caddy/data
USER 10001:10001
EXPOSE 8080
CMD ["caddy", "run", "--config", "/etc/caddy/Caddyfile", "--adapter", "caddyfile"]
