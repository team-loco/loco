FROM --platform=$BUILDPLATFORM golang:1.27.3-alpine3.23@sha256:cee7b6811ec0b093ff42b3a97ddc48d1ab0258e35e7a80643fd4e32a8a610cb3 AS builder
ARG TARGETOS
ARG TARGETARCH
ARG VERSION

WORKDIR /workspace

COPY controller/go.mod controller/go.sum ./controller/
COPY k8sapi/go.mod k8sapi/go.sum ./k8sapi/

WORKDIR /workspace/controller
RUN --mount=type=cache,target=/go/pkg/mod go mod download

WORKDIR /workspace
COPY k8sapi ./k8sapi
COPY controller ./controller

WORKDIR /workspace/controller
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -ldflags "-X main.version=${VERSION}" -o ../manager ./cmd

FROM gcr.io/distroless/static:nonroot@sha256:25ac8f0c3b2334a9eb05452266bd4697864a8c8ef0ef71e5e6480d7f92868329
WORKDIR /
COPY --from=builder /workspace/manager .
USER 65532:65532

ENTRYPOINT ["/manager"]
