# PostgreSQL benchmark protocol

## Topology and resources

Run the chart's pinned PostgreSQL version with isolated instances, volumes and credentials. Match relevant chart resources, database settings, indexes, extensions, transaction isolation, connection pool sizes and timeouts. Record actual versions, storage, CPU, memory, client location and configuration; scale resource budgets separately from database size.

| Topology | Purpose |
|---|---|
| One primary | Correctness baseline and single-instance capacity |
| One primary, one streaming standby | Core's default two-instance CloudNativePG layout |
| One primary, two streaming standbys | Replication fanout and larger HA layout |

Route application traffic as Core does, through the primary. Verify primary/standby roles, replication mode and replay lag. Standbys do not increase primary write capacity; measure standby reads only if the application actually uses them, or label them as a proposed architecture. For replication-sensitive changes, measure both asynchronous and synchronous commit configurations where relevant; distinguish them from the chart defaults. For reliability audits include primary failover under load, recovery time, transaction errors and acknowledged-write durability, using an isolated local CloudNativePG cluster. Independent servers are not a replicated topology.

## Data and workload matrix

Apply the real migrations, populate related tables with deterministic, valid organization, workspace, environment, resource, deployment, placement and build relationships. Seed relevant membership, events and history with realistic cardinalities, row widths, timestamps and status distributions. Record seed, per-table counts, table/index sizes and selected tenant cardinality. Use actual constraints and indexes.

| Scale | Selected rows in the relevant high-volume relation |
|---|---|
| Tiny | 0, 1, 100 |
| Small | 1k–10k |
| Medium | 100k–1M |
| Large | 10M |
| Massive | 100M+ where local capacity supports it |

Use applicable scales, not millions of rows in tables whose domain has a small bounded cardinality. Independently grow selected tenant data and unrelated tenant data; include many small tenants, one hot tenant, skewed histories and selective versus broad filters. Exercise first and deep pages, real ordering, empty results and common parameter distributions. Measure working sets that fit and exceed shared buffers and, where feasible, available RAM.

Benchmark isolated statements, complete transactions and realistic mixed read/write traffic. Include concurrent operations on the same resource and different resources, bulk operations and background reconciliation when relevant. For write-heavy workloads, run long enough to observe checkpoints, WAL generation, autovacuum and table/index growth. Compare freshly analyzed fixtures and representative update/delete churn where relevant; do not disable maintenance or durability to improve measured numbers.

## Correctness and stability

Run existing database tests against the disposable database, with the same migrations as the timed workloads. Check actual results, ordering and pagination, transactional invariants, affected-row counts, persistence and concurrency outcomes. For write workloads, restore equivalent initial datasets for each repetition and check final invariants. Count deadlocks, serialization failures, constraint failures, retries, statement timeouts and skipped transactions separately. Do not compare a faster implementation that changes required semantics.

## Timing and load

Use [pgbench custom scripts](https://www.postgresql.org/docs/18/pgbench.html) with actual SQL, parameter selection and transaction boundaries. Do not initialize the built-in banking schema. For example, after applying migrations and seeding a disposable database, run:

```sh
pgbench --no-vacuum --file=workload.sql --protocol=prepared \
  --client=16 --jobs=4 --time=120 --log --report-per-command \
  --random-seed=42 "$BENCH_DATABASE_URL"
```

Use the protocol that matches the Go driver; distinguish prepared and simple execution where plans differ. Use unaggregated transaction logs for percentiles; statement averages from `--report-per-command` cannot provide statement percentiles. For mixed workloads, group logs by script, not just by run. Benchmark concurrency 1, 4, 16, 32 and levels around the actual connection-pool limit. Sweep fixed arrival rates with `--rate` from below capacity through saturation; report schedule lag, late/skipped transactions and achieved throughput. Report offered versus completed traffic and retry costs. Use `--connect` only for a separate connection-churn scenario.

Measure warmup separately, repeat comparable cells independently, and alternate baseline/candidate order. Target at least 1,000 successful observations per cell for warm tail latency; label sparse samples, particularly p99, as low confidence. Preserve raw samples and report nearest-rank p50/p90/p95/p99/max, throughput, failures, repetitions and variance. Avoid declaring improvements smaller than measurement noise.

Native pgbench measures database-client transactions, not API response time or pool acquisition. If the finding concerns N+1 queries, round trips, parallel execution, decoding or pool saturation, measure the real Go caller or local API separately, with actual connection limits and request latency.

## Cold and warm

- Process-cold: restart the owned PostgreSQL instance to clear shared buffers, separately accounting for connection establishment and prepared-plan warmup. The operating-system page cache can remain warm.
- Storage-cold: additionally evict only owned database files from the OS page cache in an isolated environment or use equivalent verified cold storage. Never drop the shared host cache. Shared-buffer reads alone do not establish physical disk reads; report unverified storage-cold results as process-cold.
- Warm: prime the same query/parameter mix until stable, then measure without the warmup.

Reset before each cold observation or cold batch, distinguishing first-request samples from the subsequent warming curve. Never include warmed requests in cold percentiles. Record sample counts and cache verification; report separate p90/p95/p99 for each cache state.

## Plans and resource evidence

Capture [EXPLAIN (ANALYZE, BUFFERS, WAL, SETTINGS, FORMAT JSON)](https://www.postgresql.org/docs/18/sql-explain.html) for representative small and large tenants and different parameter selectivities, before and after. Collect plans separately from timed runs because instrumentation adds overhead. EXPLAIN ANALYZE executes writes; use disposable data and roll back where appropriate, restoring the fixture for comparisons (rollback does not undo every side effect, such as sequence increments).

Inspect estimate versus actual rows and loops, scan selectivity, rows removed by filters, join fanout, repeated subplans, heap fetches, sort/hash spills, temporary I/O and WAL. Check generic versus custom prepared plans for skewed parameters. Inspect index overlap and costs to writes, storage and maintenance; a sequential scan on a small table may be optimal.

Collect per-run deltas from [pg_stat_statements](https://www.postgresql.org/docs/18/pgstatstatements.html), pg_stat_io, pg_stat_database, pg_stat_wal, pg_stat_checkpointer, pg_stat_user_tables and pg_stat_user_indexes as applicable. Record CPU, memory, I/O timing, buffer hits/reads, locks and wait events from pg_stat_activity and pg_locks, replica lag and connection utilization. Enable required instrumentation consistently on the owned instances. Treat cumulative counters as aggregate evidence, not per-request percentiles. Distinguish query execution, lock waits, connection-pool queueing, network and client bottlenecks.

## Report

Produce a complete standalone HTML report for every run or audit, including partial or failed runs. Publish it as a Claude artifact or Codex Space page, provide the link and retain machine-readable samples alongside it. Render offline without external assets.

Include an executive summary, ranked evidence-backed improvements, charts of cold/warm p90/p95/p99 and throughput across data scales and concurrency, and separate replication and recovery measurements where applicable. Include each matrix cell (pass/fail/unrun, with reasons), revision, exact configuration, seed, commands, data sizes, correctness results, latency sample counts, errors and baseline/candidate deltas. Embed expandable raw execution plans and link raw samples. Distinguish measured effects from hypotheses; do not extrapolate local timings into production SLOs.
