FROM golang:1.27.1-alpine3.23 AS builder

ENV CGO_ENABLED=0 \
    GOOS=linux \
    GOARCH=amd64

WORKDIR /app

COPY agent/go.mod agent/go.sum ./agent/
COPY gen/go/go.mod gen/go/go.sum ./gen/go/
COPY k8sapi/go.mod k8sapi/go.sum ./k8sapi/

WORKDIR /app/agent
RUN go mod download

WORKDIR /app
COPY agent ./agent
COPY gen/go ./gen/go
COPY k8sapi ./k8sapi

WORKDIR /app/agent
RUN go build -o loco-agent .

FROM gcr.io/distroless/static:nonroot
WORKDIR /

COPY --from=builder /app/agent/loco-agent .
USER 65532:65532

ENTRYPOINT ["/loco-agent"]
