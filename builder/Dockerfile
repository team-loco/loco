FROM --platform=$BUILDPLATFORM golang:1.27.4-alpine3.23@sha256:146e3b9c7c1e2ec3d3f5348c8a686151a75e1976e50efac9fc022ebe446a9a82 AS builder
ARG TARGETARCH

ENV CGO_ENABLED=0 \
    GOOS=linux \
    GOARCH=${TARGETARCH}

WORKDIR /app/builder

COPY go.mod go.sum /app/
COPY builder/go.mod builder/go.sum ./
RUN go mod download || go mod download

COPY internal/buildinfo /app/internal/buildinfo
COPY internal/workloadplatform /app/internal/workloadplatform
COPY builder ./
# Declared after the module download, which a new VERSION leaves cached.
ARG VERSION
RUN --mount=type=cache,target=/root/.cache/go-build go build -trimpath -ldflags "-X main.version=${VERSION}" -o /loco-builder .

FROM gcr.io/distroless/static:nonroot@sha256:25ac8f0c3b2334a9eb05452266bd4697864a8c8ef0ef71e5e6480d7f92868329
WORKDIR /

COPY --from=builder /loco-builder /loco-builder
USER 65532:65532

ENTRYPOINT ["/loco-builder"]
