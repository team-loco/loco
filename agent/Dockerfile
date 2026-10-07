FROM golang:1.27.1-alpine3.23 AS builder
ARG TARGETARCH
ARG VERSION=""

ENV CGO_ENABLED=0 \
    GOOS=linux \
    GOARCH=${TARGETARCH}

WORKDIR /app

COPY agent/go.mod agent/go.sum ./agent/
COPY go.mod go.sum ./
COPY k8sapi/go.mod k8sapi/go.sum ./k8sapi/

WORKDIR /app/agent
RUN --mount=type=cache,target=/go/pkg/mod go mod download

WORKDIR /app
COPY agent ./agent
COPY gen/go ./gen/go
COPY k8sapi ./k8sapi

WORKDIR /app/agent
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build go build -ldflags "-X main.version=${VERSION}" -o loco-agent .

FROM gcr.io/distroless/static:nonroot
WORKDIR /

COPY --from=builder /app/agent/loco-agent .
USER 65532:65532

ENTRYPOINT ["/loco-agent"]
