---
title: "Audit queries"
---

An audit query selects audit events by who acted, what they acted on, the event type and the request, and runtime events by severity, region, reason and resource. The dashboard's audit log search, `loco resource events --query` and the `query` field of `EventService.ListEvents` take the same syntax as [log queries](log-queries.md), with the fields below. The organization, workspace, resource, environment and time range stay separate request fields.

```text
@type:member.* -@actor_type:system ada
```

This query matches membership events, apart from those Loco recorded itself, whose actor or target mentions `ada`.

## Text

A word or a quoted phrase matches events where any of these contains it, ignoring case:

- the actor's name or email
- the target: the ID of the event's subject, the name, domain or secret names in its data, and the name or email of the user it is about
- the names of the resource, workspace and environment the event happened in
- the event type, such as `secret.set`, and its title on the dashboard, such as `Degraded in us-va-1`
- the region, reason and message a runtime event records, and the deployment or build ID it names

Text also matches events whose request ID equals it exactly, such as the request ID an error message shows. `%`, `_` and `\` match themselves.

## Fields

| Field | Matches |
| --- | --- |
| `@actor` | The actor's user ID or token ID, or the name or email of the user who acted, ignoring case. |
| `@actor_type` | How the actor signed in: `user`, `organization`, `workspace` or `resource` for a token, `system` for Loco, `agent` for a cluster agent. |
| `@target` | The ID of the event's subject, or the name, domain or secret name in its data, the name or email of the user it is about, or the name of the resource, workspace or environment it happened in, ignoring case. |
| `@type` | The event type, such as `deployment.created`. |
| `@request` | The request ID. |
| `@severity` | How the dashboard's Events view grades the event: `error` for a failed build or deployment or a crashed deployment, `warning` for a degraded resource, `normal` otherwise. |
| `@region` | The region the event names, or the regions it names joined with `, `, ignoring case. |
| `@reason` | The reason a runtime event records, such as `out_of_memory`, ignoring case. |
| `@resource` | The name or slug of the resource the event happened in, ignoring case. |

`@type`, `@actor_type` and `@request` match case-sensitively. A value ending in `*` matches values that start with it: `@type:secret.*`, `@actor:ada*`. Audit fields hold no numbers, so comparisons and ranges such as `@type:>1` are rejected, and so are other field names.

`-` before a term keeps the events it does not match, including events without that value: `-@request:7f1c0a3e-2b44-4c8e-9a51-6d0e8b2f4c19` keeps events that have no request ID. `AND`, `OR` and parentheses combine terms as in [log queries](log-queries.md#combining-terms).

## Examples

| Query | Finds |
| --- | --- |
| `@actor:ada@acme.test` | everything one person did |
| `@actor_type:system OR @actor_type:agent` | events Loco and cluster agents recorded |
| `@type:secret.* DATABASE_URL` | changes to one secret |
| `@target:shop.example.com` | the history of one domain |
| `@type:member.* -@actor_type:system` | membership changes people made |
| `7f1c0a3e-2b44-4c8e-9a51-6d0e8b2f4c19` | the events of one request |

## Errors

A query the API cannot parse fails with `InvalidArgument`, a `google.rpc.BadRequest` violation on `query` and a `google.rpc.ErrorInfo` with reason `QUERY_SYNTAX` whose `position` metadata counts characters from 1:

```text
@type does not compare numbers at position 1
unknown field @status; use @actor, @actor_type, @target, @type, @request, @severity, @region, @reason or @resource at position 5
```
