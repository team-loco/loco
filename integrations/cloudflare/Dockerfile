FROM --platform=$BUILDPLATFORM golang:1.27.4-alpine3.23@sha256:146e3b9c7c1e2ec3d3f5348c8a686151a75e1976e50efac9fc022ebe446a9a82 AS builder
ARG TARGETARCH
ENV CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH}
WORKDIR /app
COPY go.mod go.sum ./
COPY integrations/cloudflare/go.mod integrations/cloudflare/go.sum ./integrations/cloudflare/
WORKDIR /app/integrations/cloudflare
RUN go mod download
WORKDIR /app
COPY gen/go ./gen/go
COPY internal ./internal
COPY integrations/cloudflare ./integrations/cloudflare
WORKDIR /app/integrations/cloudflare
ARG VERSION
RUN --mount=type=cache,target=/root/.cache/go-build go build -ldflags "-X main.version=${VERSION}" -o /loco-cloudflare-publisher .

FROM gcr.io/distroless/static:nonroot@sha256:25ac8f0c3b2334a9eb05452266bd4697864a8c8ef0ef71e5e6480d7f92868329
COPY --from=builder /loco-cloudflare-publisher /loco-cloudflare-publisher
USER 65532:65532
ENTRYPOINT ["/loco-cloudflare-publisher"]
