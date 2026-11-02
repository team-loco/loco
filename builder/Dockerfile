FROM --platform=$BUILDPLATFORM golang:1.27.3-alpine3.23@sha256:754d5200fc3edae76d3b6598a9a3fac7e74981ee6a5413ab955025f185753ef2 AS builder
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
