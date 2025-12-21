FROM golang:1.25.1-alpine3.22 AS builder

ENV CGO_ENABLED=0 \
    GOOS=linux \
    GOARCH=amd64

WORKDIR /app

COPY api ./api
COPY shared ./shared
COPY controller ./controller

WORKDIR /app/api

RUN go mod download

RUN go build -o loco-api .

FROM debian:bookworm-slim

WORKDIR /

COPY --from=builder /app/api/loco-api .

ENTRYPOINT ["/loco-api"]
