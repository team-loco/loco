FROM golang:1.27.1-alpine3.23 AS builder
ARG TARGETARCH

ENV CGO_ENABLED=0 \
    GOOS=linux \
    GOARCH=${TARGETARCH}

WORKDIR /app

COPY observability-proxy/go.mod observability-proxy/go.sum ./observability-proxy/
COPY go.mod go.sum ./
COPY gen/go/go.mod gen/go/go.sum ./gen/go/
COPY sdk/go/go.mod sdk/go/go.sum ./sdk/go/

WORKDIR /app/observability-proxy
RUN --mount=type=cache,target=/go/pkg/mod go mod download

WORKDIR /app
COPY observability-proxy ./observability-proxy
COPY gen/go ./gen/go
COPY sdk/go ./sdk/go

WORKDIR /app/observability-proxy
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build go build -o loco-obs-proxy .

FROM gcr.io/distroless/static:nonroot
WORKDIR /

COPY --from=builder /app/observability-proxy/loco-obs-proxy .
USER 65532:65532

ENTRYPOINT ["/loco-obs-proxy"]
