# controller

Kubernetes operator with an Application reconciler and a Build reconciler. Select one with `--reconciler=application|build`.

Generate CRDs, RBAC and deepcopy code: `mise run controller:gen`

Test: `mise run test:controller`
