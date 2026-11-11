FROM --platform=$BUILDPLATFORM golang:1.27.4-alpine3.23@sha256:146e3b9c7c1e2ec3d3f5348c8a686151a75e1976e50efac9fc022ebe446a9a82 AS builder
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
RUN --mount=type=cache,target=/go/pkg/mod go mod download || go mod download

WORKDIR /app
COPY agent ./agent
COPY gen/go ./gen/go
COPY k8sapi ./k8sapi
COPY internal ./internal

WORKDIR /app/agent
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build go build -ldflags "-X main.version=${VERSION}" -o loco-agent .

FROM gcr.io/distroless/static:nonroot@sha256:25ac8f0c3b2334a9eb05452266bd4697864a8c8ef0ef71e5e6480d7f92868329
WORKDIR /

COPY --from=builder /app/agent/loco-agent .
USER 65532:65532

ENTRYPOINT ["/loco-agent"]
