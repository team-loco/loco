FROM ghcr.io/astral-sh/uv:0.9.28 AS uv

FROM python:3.13.16-slim AS build
COPY --from=uv /uv /usr/local/bin/uv
WORKDIR /app/docs
COPY docs/pyproject.toml docs/uv.lock ./
RUN uv sync --frozen --no-dev
COPY docs/zensical.toml ./
COPY docs/scripts ./scripts
COPY docs/content ./content
COPY docs/overrides ./overrides
COPY web/public/favicon.svg /app/web/public/favicon.svg
COPY web/src/components/design/logo-strokes.ts /app/web/src/components/design/logo-strokes.ts
ARG DOCS_ENVIRONMENT=production
RUN uv run --frozen --no-dev python scripts/build.py build "$DOCS_ENVIRONMENT"

FROM ghcr.io/static-web-server/static-web-server:2.44.0
COPY docs/sws.toml /etc/sws.toml
COPY --from=build /app/docs/site /public
USER 65532:65532
EXPOSE 8080
CMD ["--config-file", "/etc/sws.toml"]
