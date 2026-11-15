---
name: db-bench
description: Measure what an API query costs Postgres (latency, CPU, buffers, WAL, memory and plan) as its rows grow from 1 to 1M, at one git ref or before and after a change. Load for any PR that touches api/queries, api/migrations or indexes, or that claims a database performance change, and put its tables in the PR body.
---

# Database benchmark

`scripts/bench.py` starts a throwaway Postgres container with the image CI's API tests use
(`.github/workflows/test.yml`), limited to `--cpus 2 --memory 2g`. For each size it applies the
migrations, seeds one tenant plus noise in other tenants, and runs the exact query text from
`api/queries/*.sql` as a prepared statement, the way pgx runs it. It removes the container
on exit, including after a failure.

```sh
mise run bench:db -- list                                   # scenarios, one per issue
mise run bench:db -- retention                              # working tree, default sizes
mise run bench:db -- list-events --ref origin/main --sizes 1,1000,100000
mise run bench:db -- all --compare origin/main HEAD         # before → after in each cell
```

`--ref` and `--compare` read migrations, queries and Go constants with `git show`, so
uncommitted changes count only without them. The tool prints an estimated runtime first.
Seeding a 1M size takes up to about 70 s. A size whose request passes the API's
statement timeout (`defaultDatabaseStatementTimeout` in `api/config.go`) is reported as
`timeout`, and the scenario skips its larger sizes.

The markdown tables go to stdout. `results.md` and `results.json` go to
`$TMPDIR/loco-dbbench/<time>/`. Never commit them.

## Reading the output

One row per size N and case. Every number is per request, averaged over 20 runs after 3
warm-ups.

- `p50 ms`, `p95 ms`: client-side time for the request, including the round trip.
- `plan ms`: planning time from `pg_stat_statements`. Near zero once the plan is cached.
- `CPU ms`: CPU the whole container used, from its cgroup, divided by requests.
- `hit`, `read`, `temp`: shared buffer hits, reads, and temp blocks read plus written, in 8 KiB
  blocks from `pg_stat_statements`. They include foreign-key cascade work and parallel workers.
- `WAL`: WAL records written. Writes run in a transaction that rolls back, so every run
  deletes the same seeded rows; WAL is still written.
- `anon MB`: the most anonymous memory the container's processes held during the runs,
  sampled every 100 ms from the cgroup's `memory.stat`. It covers backends' private memory
  such as sort, hash and parameter arrays, and leaves out page cache and shared buffers.
- `plan`: the scan node that examined the most rows in one `EXPLAIN (ANALYZE, BUFFERS)`
  run, or the foreign-key constraint whose triggers took most of the time.

`resolve-identity` (#1825) and `watch-deployment` (#1828) are rate problems, not size
problems. In `resolve-identity` N is concurrent requests from one user, each committing its
identity update. `watch-deployment` measures one poll and extrapolates `polls/s` and `DB cores`
to N streams at `watchDeploymentPollInterval`.

## Adding a scenario

Add a `Scenario` to `SCENARIOS` in `scripts/bench.py`:

- `seed(n)` returns SQL that inserts with `INSERT … SELECT generate_series`, never a row at a
  time. `BASE_SEED` already creates the tenant (`u(k, 1)` ids) and `OWNERS` other tenants.
  Other tenants get `noise(n)` rows: ten times the tenant's, capped at `NOISE_CAP`.
- `cases(n, src)` returns `{case: [(query name, params)]}`. Params are keyed by sqlc
  argument name, or by number for `$1`-style parameters. Read page sizes and intervals
  from Go with `src.go_const` instead of copying them.
- `rows(n)` estimates the seeded rows, for the runtime estimate.
- Set `commit=True` for writes that must commit, `until_empty=True` for a delete the API
  repeats until it affects no rows, `concurrency` for parallel clients, and `derive` for
  computed columns.

A fix that renames a query or its parameters updates the scenario in the same PR. Run
`mise run test:db-bench`; it checks that every scenario names an existing query and fills
every parameter.

## Lessons

Append one bullet per lesson under the topic it belongs to. Add a topic when none fits.

### Seeding

- `SET LOCAL session_replication_role = replica` skips row triggers and foreign-key checks.
  Seeding then runs at about 170k rows/s with every index in place, and can `UPDATE` the
  append-only `events` table. The seed must keep references consistent itself.
- `generate_series` yields `int4`: `g * n` overflows past 2^31 at 1M rows. Cast to `bigint`.
- `(g::bigint * n / total) <> ((g - 1)::bigint * n / total)` picks exactly n tenant rows
  spread evenly through `1..total`, so tenant and noise rows interleave in `seq` and time.
- Run `VACUUM (ANALYZE)` and `CHECKPOINT` after seeding, so the planner has statistics and the
  first runs don't pay for hint bits or a checkpoint.

### Measurement

- `EXPLAIN` takes no bind parameters ("bind message supplies 1 parameters, but prepared
  statement requires 0"). Use `EXPLAIN … EXECUTE name('literal', …)`; untyped literals take
  the prepared statement's parameter types.
- `EXPLAIN (BUFFERS)` leaves out work done in foreign-key triggers, such as a cascading
  delete. `pg_stat_statements` counts it, so the buffer columns come from there.
- A `docker exec … cat` costs the container about 5 ms of CPU, more than a fast query uses
  in 20 runs. The CPU probe is one long-lived shell that reads `cpu.stat` with builtins.
- Container CPU includes background processes such as autovacuum and the memory sampler. A
  one-row primary-key lookup reads about 0.06 ms per request; treat smaller differences as noise.
- Container memory as `docker stats` reports it is mostly page cache: WAL segments and data
  files earlier sizes left behind. It read 868 MB at N=1 right after a 1M delete. The `anon`
  line of `memory.stat` tracks the request instead: 10 MB for one secret name, 27 MB for 100k.
- The 100 ms memory sampler misses spikes in requests shorter than that.

### Planner

- `plan ms` stays above zero on most queries after 20 runs: the plan cache keeps choosing
  custom plans for them. `scope-checks` runs one statement N times per request, and its
  `plan ms` reads 0, a cached generic plan.
- A foreign-key cascade shows in `EXPLAIN` only as a trigger with its time and call count,
  never as a scan node. `retention` on main spends almost all its time in
  `webhook_deliveries_event_seq_fkey`, one call per deleted event, and times out at 10k
  events.
- Tiny tables plan as `Seq Scan` whatever the indexes; compare plans from 1k rows up.

### Pitfalls

- `--sizes` applies to every scenario. For `resolve-identity` the sizes are client
  connections; the container allows 200.
