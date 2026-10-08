FROM golang:1.27.2-alpine3.23@sha256:2ac5c2a64f1f970b5120fe21c6a5e3d9190b196a9ead95797564738ecd07a8a2 AS builder
ARG TARGETARCH
ARG VERSION

ENV CGO_ENABLED=0 \
    GOOS=linux \
    GOARCH=${TARGETARCH}

WORKDIR /app

COPY observability-proxy/go.mod observability-proxy/go.sum ./observability-proxy/
COPY go.mod go.sum ./

WORKDIR /app/observability-proxy
RUN --mount=type=cache,target=/go/pkg/mod go mod download

WORKDIR /app
COPY observability-proxy ./observability-proxy
COPY gen/go ./gen/go
COPY internal/buildinfo ./internal/buildinfo

WORKDIR /app/observability-proxy
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build go build -ldflags "-X main.version=${VERSION}" -o loco-obs-proxy .

FROM gcr.io/distroless/static:nonroot@sha256:e2e927ec666bae08560abb3c55d0659eceabb657f56b6782ab500a9fc7f555e3
WORKDIR /

COPY --from=builder /app/observability-proxy/loco-obs-proxy .
USER 65532:65532

ENTRYPOINT ["/loco-obs-proxy"]
