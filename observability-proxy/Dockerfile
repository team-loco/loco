FROM golang:1.26.0-alpine3.23 AS builder

ENV CGO_ENABLED=0 \
    GOOS=linux \
    GOARCH=amd64

WORKDIR /app

COPY observability-proxy ./observability-proxy
COPY proto ./proto

WORKDIR /app/observability-proxy

RUN go mod download

RUN go build -o loco-obs-proxy .

FROM gcr.io/distroless/static:nonroot
WORKDIR /

COPY --from=builder /app/observability-proxy/loco-obs-proxy .
USER 65532:65532

ENTRYPOINT ["/loco-obs-proxy"]
