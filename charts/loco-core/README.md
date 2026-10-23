# loco-core

`loco-core` installs the worker-cluster agent and application Gateway. Install cert-manager and the Loco operator before installing this chart, and provide the agent image tag, the control plane URL and, for the first start, a join token from `locoadm cluster create`.

The Gateway terminates TLS with a Secret in the Helm release's namespace. By default the chart creates a Let's Encrypt ACME ClusterIssuer, a wildcard Certificate, and a Cloudflare API-token Secret. Set `certManager.issuer.email` and `secrets.cloudflareToken` for this mode.

For a local installation, issue a self-signed certificate without ACME or Cloudflare:

```yaml
certManager:
  issuer:
    name: loco-local
    type: selfSigned
```

Self-signed certificates require clients to trust the certificate explicitly. They do not provide public browser trust.

To use an issuer managed outside this release, create the Certificate with that issuer's name and kind. An `Issuer` must exist in the release's namespace; a `ClusterIssuer` is cluster scoped.

```yaml
certManager:
  issuer:
    create: false
    name: organization-ca
    kind: ClusterIssuer
```

To use an existing TLS Secret, set its name. This skips the chart's Certificate, ClusterIssuer and Cloudflare Secret. The supplied Secret must exist in the release's namespace and cover every configured Gateway hostname.

```yaml
certManager:
  existingTLSSecret: gateway-tls
```


The Gateway's Envoy Service uses Envoy Gateway's defaults unless `envoyProxy.service` supplies native [EnvoyService settings](https://gateway.envoyproxy.io/docs/api/extension_types/#kubernetesservicespec). For a kind installation with host port mappings, expose HTTP on a fixed NodePort:

```yaml
envoyProxy:
  service:
    type: NodePort
    externalTrafficPolicy: Cluster
    patch:
      type: StrategicMerge
      value:
        spec:
          ports:
            - port: 80
              nodePort: 30080
```

Envoy Gateway creates and reconciles this Service. `externalTrafficPolicy: Cluster` allows a kind host mapping to reach the proxy even when the proxy pod runs on another node. Map the kind node's port `30080` to the desired host port. The patch merges the HTTP listener's generated Service port and preserves its generated target port.

`global.exporter.bearerToken.secretName` names the Secret holding the token the `loco-obs` collectors send to the control plane. It takes the same value as in `loco-obs`; Helmfile passes `env/exporter-token.yaml.gotmpl`, read from the `loco-obs` values, to both releases. With `secretKey` and the node collector's `nodeNamespace` from the same file, the agent writes the cluster's ingest token to that Secret in `global.observability.namespace` and `nodeNamespace`; the chart lets the agent create Secrets in those namespaces and `get` and `update` only that one. Without `secretName` the agent writes no token.
