# Installation file

`installation.yaml` describes a Loco installation on existing Kubernetes infrastructure. It keeps operator intent separate from application `loco.yaml` files.

!!! warning "Unreleased"
    File validation is available in the development CLI. Validation checks document structure and supported settings; it does not connect to Kubernetes, resolve Secrets, inspect release artifacts or prove installation readiness.

```sh
locoadm validate --file installation.yaml
```

The default file is `installation.yaml`. See the [complete example](https://github.com/team-loco/loco/blob/main/examples/installation/installation.yaml) and [JSON Schema](https://github.com/team-loco/loco/blob/main/schemas/installation.v1alpha1.json). Unknown fields, duplicate keys and additional YAML documents fail validation. Errors identify known field paths without printing source values.

Every field below is required, including explicit boolean settings. `workers: []` declares a control plane without attached workers. Kubernetes contexts must already exist in the operator's kubeconfig. The operator supplies PostgreSQL, OIDC, S3-compatible storage, a registry, DNS, certificate issuers and Kubernetes clusters; this file does not provision those services.

| Field | Value |
| --- | --- |
| `version` | `loco.build/admin/v1alpha1` |
| `name` | Installation identity, a lowercase Kubernetes DNS label |
| `release.manifest` | Path to a local release manifest; artifact compatibility is checked by the installer |
| `controlPlane.kubernetes.context` | Existing kubeconfig context |
| `controlPlane.kubernetes.clusterUID` | Existing cluster's `kube-system` Namespace UID; pins the target independently of the context name |
| `controlPlane.kubernetes.namespace` | Destination namespace, a lowercase Kubernetes DNS label |
| `controlPlane.apiURL`, `controlPlane.webURL` | Public API and dashboard URLs |
| `controlPlane.database.secretRef` | Database URL input Secret's `name` and `key` |
| `controlPlane.auth.issuer` | OIDC issuer URL |
| `controlPlane.auth.audience` | API's expected OIDC audience |
| `controlPlane.auth.webClientId` | Public browser client ID |
| `controlPlane.auth.bootstrapClientId` | Public OIDC client ID for operator authorization code with PKCE; must equal `audience` |
| `controlPlane.auth.bootstrapRedirectURI` | Registered loopback redirect: `http://127.0.0.1:<port>/callback`, with port 1 through 65535 |
| `controlPlane.auth.signupMode` | `open` or `closed` |
| `controlPlane.sourceBucket` | Source storage: `endpoint`, `region`, `bucket`, `credentialsSecretRef.name` |
| `controlPlane.registry.endpoint` | HTTP or HTTPS registry origin, optionally with a port; no non-root path |
| `controlPlane.registry.credentialsSecretRef.name` | Registry input Secret name |
| `controlPlane.email.from` | Sender email address, optionally with a display name |
| `controlPlane.email.transportSecretRef` | Mail transport input Secret's `name` and `key` |
| `controlPlane.openbao.managed` | `true`; external OpenBao is unsupported |
| `controlPlane.openbao.seal.type` | `static` |
| `controlPlane.openbao.snapshots` | Backup storage: `endpoint`, `region`, `bucket`, `credentialsSecretRef.name` |
| `controlPlane.observability.enabled` | `true`; disabled observability is unsupported |
| `controlPlane.observability.ingestURL` | Public OTLP ingest URL |
| `controlPlane.certificates.issuerRef` | Existing issuer's `name` and `kind` (`Issuer` or `ClusterIssuer`) |
| `workers[].name` | Unique worker identity, a lowercase Kubernetes DNS label |
| `workers[].kubernetes` | Existing `context`, pinned `clusterUID` and destination `namespace` |
| `workers[].region` | Region registered with the API |
| `workers[].tier` | `staging` or `production` |
| `workers[].appsDomain` | DNS domain for applications, without a wildcard prefix |
| `workers[].builds.enabled` | Whether this worker runs source builds |
| `workers[].builds.privateEgress` | Explicit build-network allowances as `{cidr, port}` pairs; use `[]` when none are needed |
| `workers[].certificates.issuerRef` | Existing issuer's `name` and `kind` |

URLs use HTTP or HTTPS and contain no user information, query string or fragment. Use HTTPS for public services. HTTP permits isolated local fixtures. Domains contain lowercase DNS labels. Input Secret names resolve in the control-plane context and namespace; certificate issuer references resolve at their declared target.

Store credential values in Kubernetes Secrets populated by an operator or external secret manager. The installation file accepts references only: no database password, SMTP transport, registry credential, token or arbitrary Helm values. Generated installation credentials belong in destination Secrets, outside this file. Authorized cluster administrators can still read those Secrets; avoiding routine display does not remove that trust boundary.

Builds deny private-network access by default. For a private registry or source bucket, list only the destination CIDRs and TCP ports builds need. CIDRs must be masked and canonical, ports range from 1 through 65535, and duplicate pairs fail validation. These entries match the worker controller's `privateEgress` contract; broader ranges are an explicit operator choice. DNS resolution does not create allowances automatically. With Kubernetes Services, account for the destinations your CNI evaluates after Service address translation.

Read each cluster's nonsecret identity before writing its target:

```sh
kubectl --context example-control-plane get namespace kube-system -o jsonpath='{.metadata.uid}'
```

Set `clusterUID` to that exact value. The context is the connection route; the UID identifies the external cluster. Plan and apply must reject a context that resolves to another cluster, including an empty cluster without installation state. Recreating a cluster changes this UID and requires an explicit new target configuration.

Each worker definition must pin a different physical cluster UID. Context aliases and different namespaces do not permit two worker registrations on one cluster. A control plane and one worker may share a cluster; staging and production workers require separate clusters.
