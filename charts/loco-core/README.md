# loco-core

`loco-core` installs the worker-cluster agent and application Gateway. Install cert-manager and the Loco operator before installing this chart, and provide the agent image tag and connection configuration.

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
