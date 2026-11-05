# Installation file

`installation.yaml` describes a Loco installation on existing Kubernetes infrastructure. It keeps operator intent separate from application `loco.yaml` files.

!!! warning "Unreleased"
    The development CLI provides validation, a read-only plan, installation apply and status. These commands target existing Kubernetes clusters. A running installation still requires a source deployment and HTTPS check before serving users.

```sh
locoadm validate --file installation.yaml
locoadm plan --file installation.yaml
locoadm apply --file installation.yaml
locoadm status --file installation.yaml
```

`validate` defaults to `installation.yaml`; the other commands require `--file`. See the [complete example](https://github.com/team-loco/loco/blob/main/examples/installation/installation.yaml) and [JSON Schema](https://github.com/team-loco/loco/blob/main/schemas/installation.v1.json). Unknown fields, duplicate keys and additional YAML documents fail validation. Errors identify known field paths without printing source values.

Every field below is required, including explicit boolean settings. `workers: []` declares a control plane without attached workers. Kubernetes contexts must already exist in the operator's kubeconfig. The operator supplies PostgreSQL, OIDC, S3-compatible storage, a registry, DNS, certificate issuers and Kubernetes clusters; this file does not provision those services.

| Field | Value |
| --- | --- |
| `version` | `1` |
| `name` | Installation identity, a lowercase Kubernetes DNS label |
| `release.manifest` | Path to a local release manifest; artifact compatibility is checked by the installer |
| `controlPlane.kubernetes.context` | Existing kubeconfig context |
| `controlPlane.kubernetes.clusterUID` | Existing cluster's `kube-system` Namespace UID; pins the target independently of the context name |
| `controlPlane.kubernetes.namespace` | Destination namespace, a lowercase Kubernetes DNS label |
| `controlPlane.apiURL`, `controlPlane.webURL` | Public API and dashboard URLs |
| `controlPlane.database.secretRef` | Database URL input Secret's `name` and `key` |
| `controlPlane.auth.issuer` | OIDC issuer URL |
| `controlPlane.auth.clientId` | Public OIDC client ID shared by dashboard and operator sign-in; the API accepts this ID-token audience |
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

Register one public OIDC client for authorization code with PKCE. Allow the dashboard's `webURL` origin followed by `/auth/callback` and the configured `bootstrapRedirectURI`. Dashboard and operator sign-in both send this client's ID token to the API, which validates its audience against `clientId`.

Store credential values in Kubernetes Secrets populated by an operator or external secret manager. The installation file accepts references only: no database password, SMTP transport, registry credential, token or arbitrary Helm values. Generated installation credentials belong in destination Secrets, outside this file. Authorized cluster administrators can still read those Secrets; avoiding routine display does not remove that trust boundary.

Builds deny private-network access by default. For a private registry or source bucket, list only the destination CIDRs and TCP ports builds need. CIDRs must be masked and canonical, ports range from 1 through 65535, and duplicate pairs fail validation. These entries match the worker controller's `privateEgress` contract; broader ranges are an explicit operator choice. DNS resolution does not create allowances automatically. With Kubernetes Services, account for the destinations your CNI evaluates after Service address translation.

Read each cluster's nonsecret identity before writing its target:

```sh
kubectl --context example-control-plane get namespace kube-system -o jsonpath='{.metadata.uid}'
```

Set `clusterUID` to that exact value. The context is the connection route; the UID identifies the external cluster. Plan and apply must reject a context that resolves to another cluster, including an empty cluster without installation state. Recreating a cluster changes this UID and requires an explicit new target configuration.

Each worker definition must pin a different physical cluster UID. Context aliases and different namespaces do not permit two worker registrations on one cluster. A control plane and one worker may share a cluster; staging and production workers require separate clusters.

## Release and input Secrets

`release.manifest` points to the public platform bundle's `release.json`, relative to the installation file. The JSON object contains `configVersion` matching `version`, a release `version`, its `coreCommit`, and digest-pinned `artifacts` and `images`. Required artifacts are `loco-control-plane`, `loco-worker`, `cert-manager-chart`, `loco-applications-crd`, `loco-builds-crd` and `loco-gateway-crd`. Required images are `api`, `ui`, `obs-proxy`, `agent`, `controller` and `builder`. Mutable image tags cannot replace digest references.

Create these external input Secrets in the control-plane namespace before applying:

| Reference | Required keys |
| --- | --- |
| `database.secretRef` | The declared key, containing the PostgreSQL URL |
| `sourceBucket.credentialsSecretRef` | `access-key-id`, `secret-access-key` |
| `registry.credentialsSecretRef` | `api-username`, `api-password`, `push-username`, `push-password`, `pull-username`, `pull-password` |
| `email.transportSecretRef` | The declared key, containing the supported email transport URL |
| `openbao.snapshots.credentialsSecretRef` | `access-key-id`, `secret-access-key` |

Use separate registry identities: the API identity reads and deletes build images, the push identity builds images, and the pull identity reads them. Only the corresponding push or pull credential reaches each worker. A worker never receives the complete input Secret. An HTTP registry endpoint enables the chart's insecure-registry transport; public registries should use HTTPS.

## Apply and resume

The initial installer requires a cluster-admin context for every target and checks that permission with a non-persistent SelfSubjectAccessReview; it creates no operator grant. Apply renders the pinned charts without Secrets before changing any target. It validates destinations, preserves owned credentials, establishes a destination lock, applies pinned CRDs and waits for them, and installs prerequisite controllers before installing the control plane. Compatible existing prerequisite controllers are reused only after their current replicas are available; cert-manager controller, webhook and cainjector are all gated. The Gateway controller comes from the pinned worker chart; cert-manager comes from its pinned release artifact. If the configured certificate issuer does not exist or is not Ready, apply stops with an issuer-required checkpoint. Create that issuer and run apply again. API, dashboard and ingest URLs must use the same HTTP or HTTPS scheme.

The control plane installs before API authentication is required. Apply reports the first-administrator checkpoint when bootstrap remains open. Complete the installation's authenticated bootstrap flow, then run apply again with the stored system-admin session to attach workers. Worker namespaces created by apply carry the restricted Pod Security policy. Existing worker namespaces must already match that policy; apply does not patch unrelated namespaces. Each worker registration request is persisted before its API call; a retry recovers the same registration. The agent creates and retains its private key through its existing Kubernetes identity store.

Generated seal material, the temporary bootstrap credential and ClickHouse passwords go directly to destination Secrets. Helm receives references only. Reapply preserves owned secrets and rejects malformed or missing material after credential establishment. It does not rotate keys or credentials. Changing external input credential values requires a separate rotation procedure; apply does not silently replace the API's existing copy.

Status reports the installation phase, worker enrollment readiness and required DNS hostnames with their published Gateway addresses. `running` does not establish DNS, HTTPS, a successful source build or recovery restorability. An exported recovery backup is reported separately. Removing a worker from the file reports pending removal and deletes no workload, cluster, volume or key. A recorded worker target UID cannot change on reapply.

`plan`, `apply` and `status` accept `--timeout` (30 minutes by default). Apply locks expire after that deadline plus a recovery margin; a concurrent apply fails. Secret values and raw child-process diagnostics are excluded from command output.
