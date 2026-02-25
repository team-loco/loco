FROM golang:1.26.0-alpine3.23 AS builder

ENV CGO_ENABLED=0 \
    GOOS=linux \
    GOARCH=amd64

WORKDIR /app

COPY agent ./agent
COPY proto ./proto
COPY k8sapi ./k8sapi
COPY controller ./controller

WORKDIR /app/agent

RUN go mod download

RUN go build -o loco-agent .

FROM gcr.io/distroless/static:nonroot
WORKDIR /

COPY --from=builder /app/agent/loco-agent .
USER 65532:65532

ENTRYPOINT ["/loco-agent"]
