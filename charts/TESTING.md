# Testing the charts

`loco-worker` and `loco-control-plane` have two kinds of test, the split Traefik's and ingress-nginx's charts use: static tests with [ct](https://github.com/helm/chart-testing) and unit tests with [helm-unittest](https://github.com/helm-unittest/helm-unittest). Both run on every pull request that touches `charts/`, in the Charts workflow.

mise pins every tool. `mise run setup` installs them; the static tests also need Docker.

## Test first

Change a chart in this order:

1. Add a test that describes the behaviour you want and fails on the current chart: a missing value, a bug, a feature that does not exist yet.
2. Change the chart.
3. Run the whole suite. The new test and every other test pass.

A chart change without a test that failed before it is incomplete.

## Static tests

```sh
mise run lint:charts
```

`lint:charts` first checks that each chart's `values.schema.json` matches the `# @schema` annotations in its `values.yaml`. It then runs `ct lint`, from the chart-testing image `charts/compose.yaml` pins, on every chart changed since `origin/main`:

- Chart.yaml against ct's schema
- yamllint on Chart.yaml, values.yaml and `ci/*-values.yaml`
- a version above the one on `origin/main`
- `helm lint` with each `ci/*-values.yaml`, which also validates the values against the schema

`.ct.yaml` holds the configuration. Files a chart's `.helmignore` excludes, such as `ci/` and `tests/`, need no version bump. Arguments after `--` go to `ct lint`; to lint both charts without the version check:

```sh
mise run lint:charts -- --all --check-version-increment=false
```

After editing a `# @schema` annotation, regenerate the schemas:

```sh
mise run chart:schema
```

## Unit tests

```sh
mise run test:charts
```

`test:charts` builds the chart dependencies and runs `untt`, helm-unittest's standalone binary, on both charts. Each chart keeps its suites in `tests/*_test.yaml`, one per template or feature, and the value files they share in `tests/values/`. Suites read the chart's `ci/default-values.yaml` for its required values. Address a subchart's template through its alias, for example `charts/otel-col-daemon/templates/daemonset.yaml`.

Run one suite while you work on it:

```sh
mise run helm:deps
untt -f tests/gateway_test.yaml charts/loco-worker
```

Each test asserts one property: `equal` on the field another component reads, `contains` for a list entry, `hasDocuments` for what renders, `failedTemplate` for a value the chart rejects, and `matchRegex` only where the requirement is about text. The charts use no `matchSnapshot`. A snapshot fails on every change to the rendered output, whether or not anything a reader relies on changed, and passes on any output that was snapshotted wrong.

## Collector configuration

loco-worker renders each OpenTelemetry Collector's configuration as a YAML string in the `relay` key of a ConfigMap. helm-unittest cannot assert inside a string, so the tests of that configuration are pytest in `tests/collector`, next to the test that runs the chart-pinned collector against it:

```sh
mise run test:collectors
```

The same directory holds the two tests helm-unittest cannot express: one compares the pod checksum annotation across two renders, and one compares resource quantities.

## Installation tests

The e2e suites install the charts on kind: `mise run e2e:control-plane`, `mise run e2e:certificates` and `mise run e2e:cluster-metrics` for the charts on their own, and `mise run e2e` for loco-worker with the whole platform.
