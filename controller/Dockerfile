FROM --platform=$BUILDPLATFORM golang:1.27.4-alpine3.23@sha256:146e3b9c7c1e2ec3d3f5348c8a686151a75e1976e50efac9fc022ebe446a9a82 AS builder
ARG TARGETOS
ARG TARGETARCH

WORKDIR /workspace

COPY go.mod go.sum ./
COPY controller/go.mod controller/go.sum ./controller/
COPY k8sapi/go.mod k8sapi/go.sum ./k8sapi/

WORKDIR /workspace/controller
RUN go mod download || go mod download

WORKDIR /workspace
COPY internal/buildinfo ./internal/buildinfo
COPY internal/clip ./internal/clip
COPY k8sapi ./k8sapi
COPY controller ./controller

WORKDIR /workspace/controller
# Declared after the module download, which a new VERSION leaves cached.
ARG VERSION
RUN --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -ldflags "-X main.version=${VERSION}" -o /manager ./cmd

FROM gcr.io/distroless/static:nonroot@sha256:25ac8f0c3b2334a9eb05452266bd4697864a8c8ef0ef71e5e6480d7f92868329
WORKDIR /
COPY --from=builder /manager /manager
USER 65532:65532

ENTRYPOINT ["/manager"]
