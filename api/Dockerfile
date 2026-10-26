FROM --platform=$BUILDPLATFORM golang:1.27.3-alpine3.23@sha256:7fd63dd7c37f5ddaef15bd8fd3f3e4f90f0d50cd9cae255c7cf73887a494d8df AS builder
ARG TARGETARCH
ARG VERSION

ENV CGO_ENABLED=0 \
    GOOS=linux \
    GOARCH=${TARGETARCH}

WORKDIR /app

COPY api/go.mod api/go.sum ./api/
COPY go.mod go.sum ./
COPY k8sapi/go.mod k8sapi/go.sum ./k8sapi/

WORKDIR /app/api
RUN go mod download

WORKDIR /app
COPY api ./api
COPY gen/go ./gen/go
COPY k8sapi ./k8sapi
COPY internal ./internal

WORKDIR /app/api
RUN go build -ldflags "-X main.version=${VERSION}" -o loco-api .

FROM gcr.io/distroless/static:nonroot@sha256:18f4cc26c9ffa7f20a359298be7deadac5c607c8e03a02b8119f1a5fc2f82c99
WORKDIR /

COPY --from=builder /app/api/loco-api .
USER 65532:65532

ENTRYPOINT ["/loco-api"]
