FROM golang:1.27.0-alpine3.23 AS builder

ENV CGO_ENABLED=0 \
    GOOS=linux \
    GOARCH=amd64

WORKDIR /app

COPY api ./api
COPY gen/go ./gen/go
COPY k8sapi ./k8sapi

WORKDIR /app/api

RUN go mod download

RUN go build -o loco-api .

FROM gcr.io/distroless/static:nonroot
WORKDIR /

COPY --from=builder /app/api/loco-api .
USER 65532:65532

ENTRYPOINT ["/loco-api"]
