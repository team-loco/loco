---
title: "Log queries"
---

A log query selects log lines by their text, their fields and their attributes. The dashboard's logs view, `loco resource logs --query` and the `query` field of `QueryLogs` and `TailLogs` all take the same syntax, and the API parses it.

```text
@level:error -@path:/healthz (timeout OR "connection reset")
```

This query matches error lines that are not for `/healthz` and that contain `timeout` or the phrase `connection reset`.

## Text

A word, such as `timeout`, matches lines whose message contains it. A quoted phrase, such as `"payment failed"`, matches the whole phrase. Matching is case-sensitive and finds the text anywhere in the message. Inside quotes, write `\"` for a quote and `\\` for a backslash.

## Fields

`@key:value` matches lines whose field `key` equals `value`. Keys are case-sensitive.

| Field | Matches |
| --- | --- |
| `@level` | The severity: `error`, `warn`, `info` or `debug` match their usual spellings, such as `ERROR`, `FATAL` and `Warning`, and `info` also matches lines without a severity. Any other value matches the severity text exactly. |
| `@resource` | The resource with this name, ignoring case. |
| `@trace` | The OpenTelemetry trace ID, ignoring case. |
| `@replica` | The pod name, such as `web-7c41e9-x2kd9`. |
| `@build` | The build ID of a build log. |
| `@region` | The region the line was recorded in, ignoring case. |
| any other key | The log attribute of that name, or the resource attribute when the line has no such log attribute. |

The built-in fields take precedence over attributes with the same name. Keys starting with `loco.` are reserved, apart from Kubernetes labels under `loco.io/`; use `@resource` and `@region` instead.

When a line's message is a single-line JSON object, its fields are also attributes, named by their path: `{"user":{"id":"42"},"roles":["admin"]}` gives `@user.id:42` and `@roles[0]:admin`. Attributes the line already has keep their values.

A value that contains a space, a colon, a quote or a parenthesis must be quoted: `@path:"/a:b"`. A quoted value matches exactly.

## Prefixes

A value ending in `*` matches values that start with it: `@path:/api/*` matches `/api/orders` and `/api/cart`. `*` works only at the end of an unquoted value.

## Numbers

| Query | Matches |
| --- | --- |
| `@status:>499` | greater than 499 |
| `@status:>=500` | 500 or more |
| `@duration_ms:<100` | less than 100 |
| `@duration_ms:<=100` | 100 or less |
| `@status:200..299` | from 200 to 299, both included |

A line whose field is missing or is not a number does not match a number comparison.

## Combining terms

Terms separated by spaces must all match. `AND` says the same explicitly, `OR` matches either side, and parentheses group terms. `AND` and `OR` are operators only in capitals; lower-case `and` and `or` are words to search for. Without parentheses, `AND` binds tighter than `OR`, so `a b OR c` means `(a b) OR c`.

`-` before a term, a field or a group matches the lines it does not: `-@level:debug`, `-"health check"`, `-(@method:GET OR @method:HEAD)`. A line that does not have the field matches its negation, so `-@path:/healthz` keeps lines without a path.

## Examples

| Query | Finds |
| --- | --- |
| `@level:error` | errors |
| `-@level:debug -@level:info` | everything but debug and info lines |
| `@resource:api @level:warn` | warnings from the `api` resource |
| `@method:POST @path:/api/*` | POST requests under `/api/` |
| `@status:>=500` | requests that failed with a server error |
| `@duration_ms:1000..5000` | requests that took one to five seconds |
| `@trace:4bf92f3577b34da6a3ce929d0e0e4736` | every line of one trace |
| `@user.id:42 -@level:debug` | a user's lines from a JSON message, without debug output |
| `"connection refused" OR "connection reset"` | either phrase |
| `(@level:error OR @status:>=500) -@path:/healthz` | failures outside health checks |

## Limits and errors

A query is at most 4096 bytes, with at most 100 terms and 16 levels of parentheses and negation. A query the API cannot parse fails with `InvalidArgument` and a message that names the problem and its position, counted in characters from 1:

```text
unclosed ( at position 1
quote values that contain : at position 7
```
