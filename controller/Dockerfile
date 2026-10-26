FROM --platform=$BUILDPLATFORM golang:1.27.3-alpine3.23@sha256:7fd63dd7c37f5ddaef15bd8fd3f3e4f90f0d50cd9cae255c7cf73887a494d8df AS builder
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

FROM gcr.io/distroless/static:nonroot@sha256:18f4cc26c9ffa7f20a359298be7deadac5c607c8e03a02b8119f1a5fc2f82c99
WORKDIR /
COPY --from=builder /workspace/manager .
USER 65532:65532

ENTRYPOINT ["/manager"]
