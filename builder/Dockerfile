FROM --platform=$BUILDPLATFORM golang:1.27.3-alpine3.23@sha256:cee7b6811ec0b093ff42b3a97ddc48d1ab0258e35e7a80643fd4e32a8a610cb3 AS builder
ARG TARGETARCH
ARG VERSION

ENV CGO_ENABLED=0 \
    GOOS=linux \
    GOARCH=${TARGETARCH}

WORKDIR /app/builder

COPY builder/go.mod builder/go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY builder ./
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build go build -trimpath -ldflags "-X main.version=${VERSION}" -o loco-builder .

FROM gcr.io/distroless/static:nonroot@sha256:25ac8f0c3b2334a9eb05452266bd4697864a8c8ef0ef71e5e6480d7f92868329
WORKDIR /

COPY --from=builder /app/builder/loco-builder .
USER 65532:65532

ENTRYPOINT ["/loco-builder"]
