FROM golang:1.27.1-alpine3.23 AS builder
ARG TARGETARCH

ENV CGO_ENABLED=0 \
    GOOS=linux \
    GOARCH=${TARGETARCH}

WORKDIR /app

COPY api/go.mod api/go.sum ./api/
COPY go.mod go.sum ./
COPY gen/go/go.mod gen/go/go.sum ./gen/go/
COPY k8sapi/go.mod k8sapi/go.sum ./k8sapi/

WORKDIR /app/api
RUN go mod download

WORKDIR /app
COPY api ./api
COPY gen/go ./gen/go
COPY k8sapi ./k8sapi

WORKDIR /app/api
RUN go build -o loco-api .

FROM gcr.io/distroless/static:nonroot
WORKDIR /

COPY --from=builder /app/api/loco-api .
USER 65532:65532

ENTRYPOINT ["/loco-api"]
