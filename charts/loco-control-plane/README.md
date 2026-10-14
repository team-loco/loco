# loco-control-plane (work in progress)

This chart will deploy the services that run on the Loco control plane. It currently holds only the observability proxy's manifests.

It is not shipped: no Helmfile release, Tilt resource or platform bundle installs it, and `tools/platform-release` does not package it. Its values, templates and tests are not yet exercised against a running control plane, so expect them to change before it is wired up.
