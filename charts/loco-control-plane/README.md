# loco-control-plane (work in progress)

This chart will deploy the services that run on the Loco control plane. It currently holds only the observability proxy's manifests.

It is not shipped: no Helmfile release, Tilt resource or platform bundle installs it, and `tools/platform-release` does not package it. Its values, templates and tests are not yet exercised against a running control plane, so expect them to change before it is wired up.

The proxy's HTTPRoute matches `/loco.observability.v1.ObservabilityProxyService/` on `controlPlaneHostname`, the same public hostname used for API RPCs. The proxy remains a separate service; its ClickHouse reader/migrator DSNs and its permission-check API URL are private configuration. This chart does not provide a telemetry ingestion endpoint or cross-region query routing.

Local Tilt runs the proxy as a separate host process with a private ClickHouse port-forward. The API HTTP mux forwards only the observability RPC prefix to `OBSERVABILITY_PROXY_URL`, preserving streaming and cancellation; it does not implement telemetry queries. `mise run tilt` supplies that URL from the checkout's stack ports and `mise run obs:serve` reads database credentials from the local chart's Kubernetes Secrets. Other API RPCs continue through the API server. The public query address remains `PUBLIC_API_URL`; neither the private proxy URL nor the ClickHouse DSNs are advertised to clients.
