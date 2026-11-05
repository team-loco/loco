# Chart Guidelines

Run mise commands from the repository root.

- Each chart change needs a helm-unittest test in the chart's `tests/` that fails before
  the change and passes after it. Assert the changed property; do not use `matchSnapshot`.
  See [TESTING.md](TESTING.md) for `mise run lint:charts`, `mise run test:charts` and the
  collector configuration tests that use pytest.
- Bump the chart version when a packaged file changes; `lint:charts` enforces this.
  `tests/` and `ci/` are not packaged.
- After changing `values.yaml` schema annotations, run `mise run chart:schema`.
  Do not edit generated `values.schema.json` files directly.
- Charts do not install CRDs. Before installing charts, run `mise run crds:apply` to apply
  Loco and Gateway API CRDs.
