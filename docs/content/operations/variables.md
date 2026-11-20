---
title: "Variables and secrets"
---

A resource runs with **variables**: names and values such as `LOG_LEVEL=info` that its container sees as environment variables. Values that must stay hidden, such as an API key or a database password, are **secrets**. Variables belong to a resource in one environment. Secrets belong to an environment, and any resource in it can read one.

## Variables

Variables are plain configuration. Loco stores them as written, records them with each deployment, and shows them to anyone who can read the resource. Set them in the `env` map of [loco.yaml](../deployment/loco-yaml.md#variables-and-secrets), or in the **Variables** section of a resource's settings in the dashboard. Saving a change there redeploys every region of the environment.

## Secrets

A secret is a named value of one environment. Loco encrypts it before storing it, with the environment's data key, which the [secrets key provider](secrets-key-provider.md) wraps. No API returns a value: the dashboard and the CLI show a secret's name, its version, who set it last and the resources that use it.

Manage secrets on the **Secrets** page of the dashboard, which lists the secrets of the selected environment, or with the CLI:

```sh
loco secret set --env production STRIPE_API_KEY=sk_live_...
loco secret push --env production .env
loco secret list --env production
loco secret unset --env production STRIPE_API_KEY
```

Setting, importing and deleting secrets needs write access to the workspace; listing them needs read access. A resource reads a changed value on its next deploy or restart. A secret that the latest deployment of a resource reads cannot be deleted: the dashboard disables **Delete** and names those resources, and the API and `loco secret unset` refuse it. Remove the reference from their variables and deploy them first.

## References

A variable reads a secret by referring to it as `${{ secrets.NAME }}`:

```yaml
env:
  STRIPE_API_KEY: "${{ secrets.STRIPE_API_KEY }}"
  DATABASE_URL: "postgres://app:${{ secrets.DB_PASSWORD }}@db:5432/app"
```

A variable can also refer to an output of another resource in the workspace as `${{ resource.KEY }}`, where `resource` is the resource's slug. The slug `secrets` is reserved for references to secrets. Every resource provides these outputs, which the dashboard lists under **Connection variables** in its settings:

| Output | Value |
|---|---|
| `PRIVATE_HOST` | the resource's slug, such as `api` |
| `PORT` | its port; only a resource with a port provides it |
| `PRIVATE_URL` | `http://<slug>:<port>`; only a resource with a port provides it |
| `PUBLIC_URL` | `https://` and its primary domain, the first entry of its `domains` in loco.yaml; only a resource with a domain provides it |

Any other key names one of that resource's variables, as its latest deployment in the environment runs them. In the dashboard, **Add reference** picks a secret or an output, and each value shows what it resolves to, or why it does not resolve, as you type. There is no escape for `${{`: every `${{ … }}` in a value is a reference.

References resolve when a deployment is created. A variable whose resolved value contains a secret reaches the container through a Kubernetes Secret; the deployment record keeps the reference, never the value. A deployment is refused when a reference names a secret the environment does not have, a resource the workspace does not have, a key the resource does not provide, or when references form a cycle. Deploying a resource whose variables refer to another resource needs read access to that resource, not to the resources it refers to in turn. Deploying a resource whose variables read a secret needs read access to the workspace.
