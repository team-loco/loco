---
title: "Installation file"
---

`loco-install.yaml` describes a Loco installation on existing Kubernetes infrastructure. It keeps operator intent separate from application `loco.yaml` files.

> **Unreleased**
>
> The development CLI provides validation, a read-only plan, control-plane apply, operator sign-in, worker attachment and status. These commands target existing Kubernetes clusters. A running installation still requires a source deployment and HTTPS check before serving users.

```sh
locoadm validate
locoadm plan
locoadm apply
locoadm login
locoadm cluster attach
locoadm status
locoadm verify --worker staging-us --environment ENVIRONMENT_UUID
```

Every `locoadm` command that reads the installation file reads `loco-install.yaml` in the current directory; `--file` names another file. See the [complete example](https://github.com/team-loco/loco/blob/main/examples/installation/loco-install.yaml) and [JSON Schema](https://github.com/team-loco/loco/blob/main/schemas/installation.v1.json). Unknown fields, duplicate keys and additional YAML documents fail validation. Errors identify known field paths without printing source values.

Every field below except `release`, `controlPlane.tls` and `workers[].tls` is required, including explicit boolean settings. `workers: []` declares a control plane without attached workers. Kubernetes contexts must already exist in the operator's kubeconfig. The operator supplies PostgreSQL, OIDC, S3-compatible storage, a registry, DNS and Kubernetes clusters; this file does not provision those services. Certificate issuers come from the operator or, for a self-signed issuer, from the Loco charts.

| Field | Value |
| --- | --- |
| `version` | `1` |
| `name` | Installation identity, a lowercase Kubernetes DNS label |
| `release.version` | Published platform bundle version, such as `0.1.0`; defaults to the version of `locoadm` |
| `release.manifest` | Path to a local `release.json`, for installs without registry access; replaces `release.version` |
| `controlPlane.kubernetes.context` | Existing kubeconfig context |
| `controlPlane.kubernetes.clusterUID` | Existing cluster's `kube-system` Namespace UID; pins the target independently of the context name |
| `controlPlane.kubernetes.namespace` | Destination namespace, a lowercase Kubernetes DNS label |
| `controlPlane.apiURL`, `controlPlane.webURL` | Public API and dashboard URLs |
| `controlPlane.tls.minVersion` | Optional `"1.2"` or `"1.3"`; minimum TLS version for all control-plane Gateway HTTPS listeners; omitted keeps Envoy Gateway defaults |
| `controlPlane.admins` | Emails that become system admins on their first verified sign-in; at least one |
| `controlPlane.database.secretRef` | Database URL input Secret's `name` and `key` |
| `controlPlane.auth.issuer` | OIDC issuer URL |
| `controlPlane.auth.clientId` | Public OIDC client ID shared by dashboard and operator sign-in; the API accepts this ID-token audience |
| `controlPlane.auth.signupMode` | `open` or `closed` |
| `controlPlane.sourceBucket` | Source storage: `endpoint`, `region`, `bucket`, `credentialsSecretRef.name` |
| `controlPlane.registry.endpoint` | HTTP or HTTPS registry origin, optionally with a port; no non-root path |
| `controlPlane.registry.credentialsSecretRef.name` | Registry input Secret name |
| `controlPlane.openbao.managed` | `true`; external OpenBao is unsupported |
| `controlPlane.openbao.seal.type` | `static` |
| `controlPlane.openbao.snapshots` | Backup storage: `endpoint`, `region`, `bucket`, `credentialsSecretRef.name` |
| `controlPlane.observability.enabled` | `true`; disabled observability is unsupported |
| `controlPlane.observability.ingestURL` | Public OTLP ingest URL |
| `controlPlane.certificates.issuerRef` | Existing issuer's `name` and `kind` (`Issuer` or `ClusterIssuer`); set this or `issuer` |
| `controlPlane.certificates.issuer` | `{name, selfSigned: {}}`: a self-signed ClusterIssuer the control-plane chart creates and owns; set this or `issuerRef` |
| `workers[].name` | Unique worker identity, a lowercase Kubernetes DNS label |
| `workers[].kubernetes` | Existing `context`, pinned `clusterUID` and destination `namespace` |
| `workers[].region` | Region registered with the API |
| `workers[].tier` | `staging` or `production` |
| `workers[].role` | `apps` runs only applications, `builds` only source builds, `all` both; installed as the worker chart's `cluster.role` |
| `workers[].tls.minVersion` | Optional `"1.2"` or `"1.3"`; minimum TLS version for all application and platform HTTPS listeners on this worker; omitted keeps Envoy Gateway defaults |
| `workers[].appsDomain` | DNS domain for applications, without a wildcard prefix |
| `workers[].builds.privateEgress` | Explicit build-network allowances as `{cidr, port}` pairs; use `[]` when none are needed |
| `workers[].metricsServer.enabled` | `false` uses an existing `metrics.k8s.io` provider instead of installing metrics-server |
| `workers[].metricsServer.kubeletInsecureTLS` | `true` skips kubelet certificate verification for clusters without CA-signed kubelet serving certificates |
| `workers[].certificates.issuerRef` | Existing issuer's `name` and `kind`; set this or `issuer` |
| `workers[].certificates.issuer` | `{name, selfSigned: {}}`: a self-signed ClusterIssuer the worker chart creates and owns; set this or `issuerRef` |

URLs use HTTP or HTTPS and contain no user information, query string or fragment. Use HTTPS for public services. HTTP permits isolated local fixtures. Domains contain lowercase DNS labels. Input Secret names resolve in the control-plane context and namespace; certificate issuer references resolve at their declared target.

A declared `issuer` lets a fresh install finish in one `apply`, because the chart creates the ClusterIssuer after cert-manager is installed. `selfSigned` is the only issuer type the file can declare, and clients do not trust its certificates; reference an ACME or CA issuer you manage with `issuerRef`. A worker on the control plane's cluster cannot declare an issuer with the control plane's issuer name, since one ClusterIssuer belongs to one chart release; reference it with `issuerRef: {name: <name>, kind: ClusterIssuer}` instead. Plan and apply stop when a ClusterIssuer with the declared name exists and that chart release does not own it.

Register one public OIDC client for authorization code with PKCE. Allow the dashboard's `webURL` origin followed by `/auth/callback`. Operators sign in through the dashboard too: `locoadm login` has the dashboard approve the CLI session, so the dashboard sends this client's ID token to the API, which validates its audience against `clientId`.

`controlPlane.admins` reaches the API as `LOCO_SYSTEM_ADMINS`. The first sign-in whose verified email matches an entry makes that account a system admin, also when `signupMode` is `closed`; [self-hosting](../operations/self-hosting.md#first-system-admin) describes claims and revocation. List admins only on an identity provider that verifies email addresses. Never enable Dex's `insecureSkipEmailVerified`. With GitHub Enterprise Server, Dex treats every email as verified.

Store credential values in Kubernetes Secrets populated by an operator or external secret manager. The installation file accepts references only: no database password, registry credential, token or arbitrary Helm values. Generated installation credentials belong in destination Secrets, outside this file. Authorized cluster administrators can still read those Secrets; avoiding routine display does not remove that trust boundary.

Builds deny private-network access by default. For a private registry or source bucket, list only the destination CIDRs and TCP ports builds need. CIDRs must be masked and canonical, ports range from 1 through 65535, and duplicate pairs fail validation. These entries match the worker controller's `privateEgress` contract; broader ranges are an explicit operator choice. DNS resolution does not create allowances automatically. With Kubernetes Services, account for the destinations your CNI evaluates after Service address translation.

Read each cluster's nonsecret identity before writing its target:

```sh
kubectl --context example-control-plane get namespace kube-system -o jsonpath='{.metadata.uid}'
```

Set `clusterUID` to that exact value. The context is the connection route; the UID identifies the external cluster. Plan and apply must reject a context that resolves to another cluster, including an empty cluster without installation state. Recreating a cluster changes this UID and requires an explicit new target configuration.

Each worker definition must pin a different physical cluster UID. Context aliases and different namespaces do not permit two worker registrations on one cluster. A control plane and one worker may share a cluster; staging and production workers require separate clusters.

## Release and input Secrets

`release` selects the platform bundle to install and takes exactly one of `version` or `manifest`:

```yaml
release:
  version: 0.1.0
```

With `version`, `locoadm` resolves `ghcr.io/team-loco/loco/platform:<version>` to a digest, downloads it anonymously, checks the digest, and reads `release.json` from the bundle. That file must name the same version. Without `release`, `locoadm` installs the bundle matching its own version, so `locoadm` 0.1.0 installs bundle `0.1.0`; a development build has no matching bundle and requires `release.version` or `release.manifest`. When the bundle version differs from the version of `locoadm`, the command prints a warning and continues. `plan` reports the resolved bundle as `releaseBundle`, and `status` reports the bundle the last apply recorded.

`release.manifest` points to a `release.json` copied from a platform bundle, relative to the installation file. Each release also attaches its bundle's `release.json` as a release asset. Use it where the operator machine cannot reach the bundle's registry; the artifacts and images it pins must still be pullable. `plan` and `status` report no bundle digest for a local manifest. The JSON object contains `configVersion` matching `version`, a release `version`, its `coreCommit`, and digest-pinned `artifacts` and `images`. Required artifacts are `loco-control-plane`, `loco-worker`, `cert-manager-chart`, `loco-applications-crd`, `loco-builds-crd` and `loco-gateway-crd`. Required images are `api`, `ui`, `obs-proxy`, `agent`, `controller` and `builder`. Mutable image tags cannot replace digest references.

Create these external input Secrets in the control-plane namespace before applying:

| Reference | Required keys |
| --- | --- |
| `database.secretRef` | The declared key, containing the PostgreSQL URL |
| `sourceBucket.credentialsSecretRef` | `access-key-id`, `secret-access-key` |
| `registry.credentialsSecretRef` | `api-username`, `api-password`, `push-username`, `push-password`, `pull-username`, `pull-password` |
| `openbao.snapshots.credentialsSecretRef` | `access-key-id`, `secret-access-key` |


The initial installation contract uses static registry credentials. Token-based registry signing is outside this file contract.

Use separate registry identities: the API identity reads and deletes build images, the push identity builds images, and the pull identity reads them. Only the corresponding push or pull credential reaches each worker. A worker never receives the complete input Secret. An HTTP registry endpoint enables the chart's insecure-registry transport; public registries should use HTTPS.

## Apply and resume

The initial installer requires a cluster-admin context for every target and checks that permission with a non-persistent SelfSubjectAccessReview; it creates no operator grant. Apply renders the pinned charts without Secrets before changing any target. It validates destinations, preserves owned credentials, establishes a destination lock, applies pinned CRDs and waits for them, and installs prerequisite controllers before installing the control plane. Installer-owned prerequisite releases upgrade to the pinned charts. Compatible prerequisite controllers owned elsewhere are reused only after their current replicas are available; cert-manager controller, webhook and cainjector are all gated. The Gateway controller comes from the pinned worker chart and uses its `GatewayNamespace` deployment configuration. Reused controllers must have that mode, the pinned controller identity and the default all-namespace watch scope in their mounted configuration so they handle Loco's Gateways and keep proxies inside the namespace application ingress policies allow. Apply rejects incompatible controllers owned elsewhere. Cert-manager comes from its pinned release artifact. If a referenced certificate issuer (`issuerRef`) does not exist or is not Ready, apply stops with an issuer-required checkpoint. Create that issuer and run apply again. API, dashboard and ingest URLs must use the same HTTP or HTTPS scheme.

Apply installs or upgrades the control plane and never needs an API session. Select the target `release.version` or `release.manifest` and run apply to update CRDs, installer-owned prerequisites and the control-plane chart in place. The state ConfigMap records the requested `releaseVersion`, `releaseCommit` and `releaseBundle` under the destination lock; a failed apply can resume with that release. Its configuration version must match the CLI contract. After apply succeeds, run `locoadm cluster attach` with the same installation file to upgrade workers to the recorded release while retaining their cluster registrations and keys. It then reports the configured admins nobody has claimed (every configured admin when no system-admin session is saved) and the next two commands:

```sh
locoadm login
locoadm cluster attach
```

`locoadm login` signs in to `controlPlane.apiURL` through the dashboard at `controlPlane.webURL`, like `loco login`, and saves the same session, so `loco` and `locoadm` share it. `--device` prints a code to approve on another device, for machines without a browser. Sign in as one of `controlPlane.admins` to claim it.

`locoadm cluster attach` needs a system-admin session. For every worker in the file, or only the one named (`locoadm cluster attach staging-us`), it registers the cluster, issues the join token, stores the enrollment Secret and installs `loco-worker`, then waits for the agent to report. Rerunning it on an attached worker upgrades its chart without enrolling it again. Worker namespaces it creates carry the restricted Pod Security policy. Existing worker namespaces must already match that policy; attach does not patch unrelated namespaces. Each worker registration request is persisted before its API call; a retry recovers the same registration. The agent creates and retains its private key through its existing Kubernetes identity store. `locoadm cluster create` remains for operators who install the agent themselves.

Generated seal material and ClickHouse user and operator credentials go directly to destination Secrets. The operator's Secret name and username come from the pinned chart. Helm receives references only. Reapply preserves owned secrets and rejects malformed or missing material after credential establishment. It does not rotate keys or credentials. Changing external input credential values requires a separate rotation procedure; apply does not silently replace the API's existing copy.

Status reports the installation phase, worker enrollment readiness and required DNS hostnames with their published Gateway addresses. With a system-admin session, `admins.unclaimed` lists the configured admins nobody has claimed; without one, `admins.checked` is `false`. `running` does not establish DNS, HTTPS, a successful source build or recovery restorability. An exported recovery backup is reported separately. Removing a worker from the file reports pending removal and deletes no workload, cluster, volume or key. A recorded worker target UID cannot change on reapply.

`plan`, `apply`, `status` and `cluster attach` accept `--timeout` (30 minutes by default). Apply locks use API-server time sampled through a non-persistent dry-run ConfigMap creation and expire after the operation deadline plus a recovery margin; a concurrent apply fails. Operator wall-clock skew does not determine expiry. Lease recovery requires a stable, synchronized control-plane clock. Expiry does not roll back previously accepted remote operations; reapply relies on owned state, atomic creation and resource-version checks to preserve keys and identities. Secret values and raw child-process diagnostics are excluded from command output.

## Source verification

`locoadm verify --worker staging-us --environment ENVIRONMENT_UUID` uses the stored operator session and an existing authorized environment whose tier matches the worker. The worker must enable builds. DNS and an HTTPS certificate must already serve its application domain. The HTTPS checks trust the operating system's certificate roots; `--ca-file PATH` adds the PEM certificates in that file for a domain whose certificate comes from a private or self-signed CA. `SSL_CERT_FILE` has no effect on macOS. The flag applies only to the application-domain checks; API requests and the source upload use the system roots.

Verification provisions a uniquely named disposable service through the plan API, stores a random application secret, uploads source and waits for its build. It deploys that build in the worker's region and checks the HTTPS response, the SHA-256 hash of the delivered secret, and a unique log record through the observability query API. The generated source contains no secret value. The API schedules the build on an eligible build cluster; the command does not pin the build executor to the selected worker. Verification deletes its service after success or failure and reports cleanup errors. A process crash can leave a `verify-<UUID>` resource for an operator to remove.

The command reports `verified` only after every probe and cleanup succeeds. This proves source build, secret delivery, deployment, HTTPS and log query functionality; it does not prove backups can restore the installation.

## Encrypted recovery export

`locoadm recovery export` reads the existing destination material, encrypts it in memory and writes only age ciphertext to a new directory:

```sh
locoadm recovery export --output ./recovery-export \
  --recipient AGE_MATERIAL_RECIPIENT \
  --share-recipient AGE_FIRST_CUSTODIAN \
  --share-recipient AGE_SECOND_CUSTODIAN \
  --share-recipient AGE_THIRD_CUSTODIAN \
  --share-recipient AGE_FOURTH_CUSTODIAN \
  --share-recipient AGE_FIFTH_CUSTODIAN
```

Supply one distinct age X25519 public recipient per recovery share, in share order, and a separate recipient for the seal material and generate-root token. Match the share count in the pinned control-plane chart. Keep private age identities with their custodians. `material.age` contains the seal key and generate-root token; `share-1.age`, `share-2.age` and subsequent files each contain one recovery share. No recipient receives multiple payloads. The directory uses mode `0700` and files use `0600`; an existing output directory is rejected. Retry a failed export with a new directory.

| Material | Destination owner | Export |
| --- | --- | --- |
| OpenBao seal key | Installation-owned `loco-openbao-seal` Secret | `material.age` |
| Generate-root token | Chart setup's `loco-control-plane-openbao-init` Secret | `material.age` |
| Recovery shares | Chart setup's `loco-control-plane-openbao-init` Secret | One encrypted file per custodian |
| Agent private keys | Each worker agent's identity store | Excluded |
| Database, storage, registry credentials | Operator-managed input Secrets | Excluded |

Export retains the source Secrets and records `recoveryExported` only after encrypted files are written and synced. That marker confirms export, not successful restoration. Keep source material until a separate restoration exercise succeeds. The export does not include PostgreSQL data, OpenBao snapshots, persistent volumes or external-service backups; retain those through their own backup procedures. This command does not implement restore.
