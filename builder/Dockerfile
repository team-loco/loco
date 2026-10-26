FROM --platform=$BUILDPLATFORM golang:1.27.3-alpine3.23@sha256:7fd63dd7c37f5ddaef15bd8fd3f3e4f90f0d50cd9cae255c7cf73887a494d8df AS builder
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

FROM gcr.io/distroless/static:nonroot@sha256:18f4cc26c9ffa7f20a359298be7deadac5c607c8e03a02b8119f1a5fc2f82c99
WORKDIR /

COPY --from=builder /app/builder/loco-builder .
USER 65532:65532

ENTRYPOINT ["/loco-builder"]
