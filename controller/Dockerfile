FROM golang:1.27.2-alpine3.23@sha256:9e45f0eb4a63ed37ad6e604950407558b7c738e34e015ae77a5d4ad369cde079 AS builder
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

FROM gcr.io/distroless/static:nonroot@sha256:e2e927ec666bae08560abb3c55d0659eceabb657f56b6782ab500a9fc7f555e3
WORKDIR /
COPY --from=builder /workspace/manager .
USER 65532:65532

ENTRYPOINT ["/manager"]
