FROM --platform=$BUILDPLATFORM golang:1.27.3-alpine3.23@sha256:cee7b6811ec0b093ff42b3a97ddc48d1ab0258e35e7a80643fd4e32a8a610cb3 AS build
ARG TARGETARCH
ARG VERSION
WORKDIR /src
COPY tests/e2e/fixtures/api-failover/main.go .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} go build -ldflags "-X main.version=${VERSION}" -o /server main.go

FROM gcr.io/distroless/static:nonroot@sha256:25ac8f0c3b2334a9eb05452266bd4697864a8c8ef0ef71e5e6480d7f92868329
COPY --from=build /server /server
ENTRYPOINT ["/server"]
