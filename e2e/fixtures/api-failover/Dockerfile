FROM --platform=$BUILDPLATFORM golang:1.27.3-alpine3.23@sha256:7fd63dd7c37f5ddaef15bd8fd3f3e4f90f0d50cd9cae255c7cf73887a494d8df AS build
ARG TARGETARCH
ARG VERSION
WORKDIR /src
COPY e2e/fixtures/api-failover/main.go .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} go build -ldflags "-X main.version=${VERSION}" -o /server main.go

FROM gcr.io/distroless/static:nonroot@sha256:18f4cc26c9ffa7f20a359298be7deadac5c607c8e03a02b8119f1a5fc2f82c99
COPY --from=build /server /server
ENTRYPOINT ["/server"]
