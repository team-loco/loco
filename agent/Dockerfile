FROM --platform=$BUILDPLATFORM golang:1.27.2-alpine3.23@sha256:9e45f0eb4a63ed37ad6e604950407558b7c738e34e015ae77a5d4ad369cde079 AS builder
ARG TARGETARCH
ARG VERSION

ENV CGO_ENABLED=0 \
    GOOS=linux \
    GOARCH=${TARGETARCH}

WORKDIR /app

COPY agent/go.mod agent/go.sum ./agent/
COPY go.mod go.sum ./
COPY k8sapi/go.mod k8sapi/go.sum ./k8sapi/

WORKDIR /app/agent
RUN --mount=type=cache,target=/go/pkg/mod go mod download

WORKDIR /app
COPY agent ./agent
COPY gen/go ./gen/go
COPY k8sapi ./k8sapi
COPY internal/buildinfo ./internal/buildinfo
COPY internal/httputil ./internal/httputil
COPY internal/rpc ./internal/rpc
COPY internal/loglevel ./internal/loglevel

WORKDIR /app/agent
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build go build -ldflags "-X main.version=${VERSION}" -o loco-agent .

FROM gcr.io/distroless/static:nonroot@sha256:e2e927ec666bae08560abb3c55d0659eceabb657f56b6782ab500a9fc7f555e3
WORKDIR /

COPY --from=builder /app/agent/loco-agent .
USER 65532:65532

ENTRYPOINT ["/loco-agent"]
