# Installation acceptance

`mise run e2e:installation` provisions disposable infrastructure, then runs the public `locoadm` and `loco` commands from an operator pod with an explicit kubeconfig. The Go runner lives in `tools/installation-e2e`; these files own its external fixtures.

The suite uses a real Dex password login through authorization code with PKCE, closed-signup first-admin bootstrap, chart-managed OpenBao and observability, an in-cluster staging agent, authenticated build push/pull and CA-verified HTTPS. `locoadm verify` then creates its own source service and verifies its secret and log delivery. Fixture DNS points at real cloud-provider-kind LoadBalancers, including wildcard application routing.

Recovery acceptance exports encrypted material to distinct throwaway age custodians and decrypts it only in memory. A disposable OpenBao instance restores the raft snapshot using the exported static seal key, restarts to exercise auto-unseal, decrypts a ciphertext created before the snapshot and signs data with the original ingest identity. Recovery export status and restore success are separate checks. Temporary roots are revoked and disposable resources are removed after success or failure.

Run once with `--no-teardown` for development, then reuse the kept infrastructure with `--skip-build`. The default path removes the fixture on success and failure. No SQL creates users, sessions or cluster registrations, and the harness does not provide generated Loco credentials or key material. External fixture credentials remain development-only inputs. The operator image's Docker context allowlist excludes run directories, kubeconfigs and local credential files.

`--timeout` bounds host setup and acceptance; its default leaves time for setup and cleanup within the CI job. Cleanup has its own deadline even when acceptance times out.

| Requirement | Evidence in this suite |
| --- | --- |
| [719](https://github.com/team-loco/loco/issues/719) | Public installation, real IdP authentication and enrolled in-cluster worker |
| [968](https://github.com/team-loco/loco/issues/968) | Dedicated installation CI job and installer/chart/harness path filters |
| [426](https://github.com/team-loco/loco/issues/426) | Cold CRD installation and real certificate-issuer readiness checkpoint |
| [716](https://github.com/team-loco/loco/issues/716) | Docker builds explicitly target the Docker daemon's platform, including ARM64 |
| [926](https://github.com/team-loco/loco/issues/926) | Functional build-network allow/deny probe; broader health diagnosis remains outside this suite |
| [971](https://github.com/team-loco/loco/issues/971) | Fresh installation from the explicit file contract, followed by source deployment |
| [1026](https://github.com/team-loco/loco/issues/1026) | Product verification sets a fresh secret through the public API and checks its hash through HTTPS after OpenBao/agent delivery |
| [1195](https://github.com/team-loco/loco/issues/1195) | Runner tests require failed acceptance and cleanup failures to remain failures |
| [1196](https://github.com/team-loco/loco/issues/1196) | Unknown options and test filters fail before infrastructure creation |

These links trace acceptance requirements; they do not claim each issue is closed by this suite. The legacy component runners retain their own coverage and migration work.
