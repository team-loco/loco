# The installation suite's operator pod: locoadm, loco and the suite's helm and
# kubectl wrappers. HELM_VERSION and KUBECTL_VERSION come from mise.toml.
FROM --platform=$BUILDPLATFORM golang:1.27.4-alpine3.23@sha256:146e3b9c7c1e2ec3d3f5348c8a686151a75e1976e50efac9fc022ebe446a9a82 AS builder
ARG TARGETARCH
ENV CGO_ENABLED=0 \
    GOOS=linux \
    GOARCH=${TARGETARCH}
WORKDIR /src
COPY go.mod go.sum ./
COPY cmd/go.mod cmd/go.sum ./cmd/
RUN go mod download && go -C cmd mod download
COPY . .
ARG VERSION
RUN go -C cmd build -ldflags "-X main.version=${VERSION}" -o /loco ./loco \
 && go build -o /installation-e2e ./tools/installation-e2e

FROM golang:1.27.4-alpine3.23@sha256:146e3b9c7c1e2ec3d3f5348c8a686151a75e1976e50efac9fc022ebe446a9a82 AS operator-tools
SHELL ["/bin/ash", "-o", "pipefail", "-c"]
ARG TARGETARCH
ARG HELM_VERSION
ARG KUBECTL_VERSION
WORKDIR /tmp
RUN wget -qO /tmp/helm.tgz "https://get.helm.sh/helm-v${HELM_VERSION}-linux-${TARGETARCH}.tar.gz" \
 && wget -qO /tmp/helm.sha "https://get.helm.sh/helm-v${HELM_VERSION}-linux-${TARGETARCH}.tar.gz.sha256sum" \
 && sed -i 's|helm-v[^ ]*|helm.tgz|' helm.sha \
 && sha256sum -c helm.sha \
 && tar -xzf helm.tgz \
 && cp "linux-${TARGETARCH}/helm" /helm
RUN wget -qO /kubectl "https://dl.k8s.io/release/v${KUBECTL_VERSION}/bin/linux/${TARGETARCH}/kubectl" \
 && wget -qO /tmp/kubectl.sha "https://dl.k8s.io/release/v${KUBECTL_VERSION}/bin/linux/${TARGETARCH}/kubectl.sha256" \
 && echo "$(cat /tmp/kubectl.sha)  /kubectl" | sha256sum -c - \
 && chmod +x /kubectl

FROM golang:1.27.4-alpine3.23@sha256:146e3b9c7c1e2ec3d3f5348c8a686151a75e1976e50efac9fc022ebe446a9a82
WORKDIR /tmp
COPY --from=operator-tools /helm /kubectl /usr/local/libexec/
RUN ln -s /usr/local/bin/installation-e2e /usr/local/bin/helm \
 && ln -s /usr/local/bin/installation-e2e /usr/local/bin/kubectl
COPY --from=builder /loco /installation-e2e /usr/local/bin/
COPY tests/e2e/fixtures/build-app /fixture/build-app
COPY tests/e2e/fixtures/installation/network-probe.yaml /fixture/network-probe.yaml
COPY tests/e2e/fixtures/installation/recovery.yaml /fixture/recovery.yaml
CMD ["sleep", "infinity"]
