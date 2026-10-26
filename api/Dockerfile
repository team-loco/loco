FROM --platform=$BUILDPLATFORM golang:1.27.3-alpine3.23@sha256:754d5200fc3edae76d3b6598a9a3fac7e74981ee6a5413ab955025f185753ef2 AS builder
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

FROM gcr.io/distroless/static:nonroot@sha256:e2e927ec666bae08560abb3c55d0659eceabb657f56b6782ab500a9fc7f555e3
WORKDIR /

COPY --from=builder /app/api/loco-api .
USER 65532:65532

ENTRYPOINT ["/loco-api"]
