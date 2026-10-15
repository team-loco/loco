# Platform image architectures

Platform image publishing builds `linux/amd64` and `linux/arm64` variants from one commit and publishes one immutable image index for each image tag. This includes API, agent, controller, builder, observability proxy, production UI and staging UI.

Go build stages run on `BUILDPLATFORM` with `CGO_ENABLED=0` and the requested `TARGETARCH`; runtime stages select the target architecture from their pinned base indexes. UI and documentation stages also run on `BUILDPLATFORM`, and copy architecture-independent static assets into the target static web server image. No target-side build commands require QEMU. BuildKit runs in its separately pinned `moby/buildkit` container; the Loco builder image contains the Go source-transfer and registry helpers, rather than a downloaded architecture-specific BuildKit executable.

The Docker workflow builds every image on native AMD64 and ARM64 GitHub runners. It checks the loaded image's OS/architecture, extracts its entrypoint and verifies the ELF machine and static linkage. Publishing repeats those checks on each immutable platform manifest, creates provenance and an SBOM for each platform digest, and attests the image index after both platforms pass. Platform release tooling rejects an index without exactly one Linux manifest for each supported architecture before publishing any release artifacts. Charts continue to pin the resulting index digest, so each node selects its architecture without changing the release identity.

Run the offline selection, executable-format and version-pin tests with:

```sh
mise run test:platform-images
mise run test:platform-binaries
mise run test:platform-release
mise run lint:actions
mise run lint:versions
mise run lint:docker
```

A platform image's architecture does not determine the architectures of application images. The API's existing application-image validation requires an AMD64 variant; an ARM64-only application image remains outside that contract. These image checks also do not replace a complete ARM64 cluster deployment, build and observability E2E run.
