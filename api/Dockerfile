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

FROM gcr.io/distroless/static:nonroot
WORKDIR /

COPY --from=builder /app/api/loco-api .
USER 65532:65532

ENTRYPOINT ["/loco-api"]
