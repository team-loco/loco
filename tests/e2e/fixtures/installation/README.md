# Installation acceptance

`mise run e2e:installation` installs a platform bundle through Flux on a disposable kind cluster, upgrades it, and runs the public operator and application commands. `tools/installation-e2e` owns the runner; this directory owns its external fixtures.

The baseline uses the merge base's charts and images, packaged by the head's bundle tooling. The head supplies the upgrade bundle. [2186](https://github.com/team-loco/loco/issues/2186) tracks selecting the newest published v2 bundle as the baseline. Both generations use digest-pinned artifacts. The baseline worktree lives beside the tested checkout.

Flux reconciles the bundle's control-plane entry point, then its combined entry point after registration. The operator signs in with the device flow and a verified Dex identity under closed signup. `locoadm cluster create -o secret` prints a Kubernetes Secret manifest that the harness applies directly. Secret encryption and Flux decryption belong to operator tooling and are outside this test.

Upgrade checks compare agent identity, seal material, initialization material and ClickHouse credentials before and after reconciliation. A transit ciphertext created before the upgrade must decrypt afterwards, and the ingest signing key must retain its public identity. A fresh operator login and a 72-hour token issuance exercise the upgraded API.

The application workflow builds source, checks CA-verified HTTPS and enforces build-network isolation. `locoadm verify` creates its own source service and checks secret and log delivery. Fixture DNS points at cloud-provider-kind LoadBalancers, including wildcard application routing.

Recovery exports ciphertext to separate throwaway age custodians. A disposable OpenBao instance restores the raft snapshot with the exported seal key, restarts to exercise auto-unseal, decrypts an existing ciphertext and signs data with the original ingest identity. Temporary roots are revoked and disposable resources are removed after success or failure.

`--no-teardown` keeps the fixture for inspection. `--skip-build` reuses built images. The default path removes the fixture and baseline worktree after success or failure. External credentials are public test identities. The operator image's Docker context excludes run directories, kubeconfigs and local credentials.

`--timeout` bounds host setup and acceptance. Cleanup has a separate deadline after cancellation. Failures report allowlisted build, pod, event and node states plus numeric capacity; specs and provider messages stay private.
