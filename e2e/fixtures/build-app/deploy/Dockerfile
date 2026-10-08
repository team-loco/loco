FROM golang:1.27.2-alpine3.23@sha256:2ac5c2a64f1f970b5120fe21c6a5e3d9190b196a9ead95797564738ecd07a8a2 AS build
WORKDIR /src
COPY go.mod ./
COPY main.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/server .

FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/server /server
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/server"]
