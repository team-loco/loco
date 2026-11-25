# ClickHouse benchmark protocol

## Scope and topology

Use the ClickHouse server image pinned in Core, dedicated isolated containers or a local cluster, unique resource names, and disposable volumes. Record server/client versions, CPU and memory limits per node, total limits, storage, Docker/VM environment, query settings, and merge state. Check available disk and memory before large runs. Use isolated Keeper for replication. Never flush the shared host cache or modify another local workload.

| Topology | Purpose |
|---|---|
| 1 shard, 1 replica | Baseline |
| 1 shard, 2 replicas | Replication and routing overhead |
| 2 shards, 1 replica | Horizontal partitioning |
| 2 shards, 2 replicas | Distribution plus replication |
| 4 shards, 1 replica | Larger shard fanout |
| 4 shards, 2 replicas | Larger distributed replicated layout |
| 1 shard, 3 replicas | Replica selection and resilience |
| 3 shards, 2 replicas | Non-power-of-two distribution |

Run additional production-relevant shapes if needed. Smoke-test one topology before sweeping the others. Run topology cells sequentially if local resources cannot sustain them concurrently. Compare both fixed per-node and, where meaningful, fixed aggregate resource budgets; annotate which policy is used. Replicas are not additional logical rows. Validate shard coverage, replica convergence, row counts, and the actual initiator's routing using the cluster configuration and system tables. Do not accidentally benchmark only one local shard.

Core migrations currently create MergeTree tables, not a distributed replicated schema. Use the unchanged migrations for the 1s1r baseline; for multi-node experiments explicitly document any benchmark-only local-table engine conversion and Distributed-table routing. Verify logical result parity against the baseline and label these as synthetic distributed variants, not a claim about current production configuration. Never publish a schema conversion merely to make a benchmark run.

## Workload matrix

Construct deterministic fixtures from Loco's real telemetry schema and ingest behavior. Record seed, exact row counts, partitions/parts, compression, event-time distribution, signal mix, attributes/cardinality, replica IDs, org/workspace/resource counts, and query selectivity.

| Scale | Target selected resource/workspace rows per signal or simulation |
|---|---|
| Tiny | 0, 1, 100 |
| Small | 10k |
| Medium | 100k–1M |
| Large | 10M |
| Massive | 100M+ where local capacity supports it |

Cross representative scales with small and large organizations/workspaces, single and multiple resources, uniform distribution and a hot tenant/resource. Independently increase unrelated data while keeping the selected tenant and time range fixed. Independently increase the selected tenant volume and queried time range while holding unrelated data fixed. Measure fixed logical dataset across topologies (strong scaling) and proportional data growth per shard (weak scaling); record exact logical/physical rows. Use low- and high-cardinality attributes, ordinary and selective filters, and realistic recent versus historical windows. For logs include body/attribute searches, pagination and buckets; for metrics include gauge, counter resets, histogram/quantile, grouped/ungrouped series; for traces include span counts, filtering, summaries, detail and buckets. Select only dimensions relevant to the query, but explicitly record which were excluded and why.

Seed using batches and allow merges to reach a recorded comparable steady state; separately benchmark ingest contention if applicable. Use actual production ingest shape, not a flat numbers table. Avoid all-identical strings or unrealistically tiny attributes.

## Correctness and stability

Run known-result checks and actual Go query integration tests before measurement. Compare baseline and candidate decoded results with deterministic ordering, pagination boundaries, empty input, duplicate timestamps, overlapping ranges, and relevant aggregate tolerances. Verify stable result cardinalities and values on each topology, repeated execution, and different replica routing. Validate exact results where required; use explicit tolerance only for intended approximate algorithms. Count failures, timeouts and missing samples rather than hiding them. Record whether repeated results vary and why. Do not use performance as a substitute for behavioral equivalence.

## Cold versus warm

Define cold in separate tiers:

- Query-cache cold: result cache disabled; clear applicable ClickHouse mark/uncompressed/filesystem caches on owned instances, using commands supported by the pinned version. Record settings and commands.
- Storage cold: additionally restart owned server instances as needed and evict owned data files from the Linux page cache using file-scoped mechanisms, or recreate isolated instances/volumes from a deterministic dataset with verifiable cold storage. Docker Desktop host/VM caches complicate this; label results as process/cache-cold or unverified storage-cold unless there is evidence of disk reads. Do not use host-global cache dropping.
- Warm: prime the exact query/parameter mix and wait for comparable steady state, then measure without including warmups. Keep result cache disabled unless intentionally benchmarking it as a separate scenario.

Perform the appropriate reset before every cold sample, not merely once before a mixed run. Record disk reads/cache-hit ProfileEvents when available and whether the reset was verified. Do not mix warm and cold samples into one percentile. Where storage-cold repetitions are costly, do not silently substitute warm samples; report the available sample count and limitation.

## Timing and tools

[clickhouse-benchmark](https://clickhouse.com/docs/concepts/features/tools-and-utilities/clickhouse-benchmark) is the default for SQL latency and load. Check local binary `--help`: newer options may not be in the pinned version. Example inside the owned initiator container, using only synthetic benchmark credentials and one fixed query:

```sh
clickhouse-benchmark --host=localhost --database=bench --query='SELECT count() FROM bench.otel_logs' --iterations=1000 --concurrency=1 --delay=0 --client-side-time
```

This example is a tool smoke test only, not a Loco query benchmark. Use the real query/arguments in actual runs. Capture server-time and client-time separately, and distinguish native client latency from actual Go application latency. For warm runs, collect 1,000+ successful observations per cell where practical; use at least 100 for an exploratory p99 and label it low confidence. Report raw sample count, nearest-rank p50/p90/p95/p99/max, variation across repetitions and failures. Run several independent repetitions and interleave baseline/candidate order; avoid claiming wins inside observed variance. Use concurrency 1, 4, 16, 32 or workload-specific levels, and fixed-arrival-rate tests if the pinned utility supports them. Closed-loop throughput understates queueing under overload; native open-loop reports can exclude client queue wait, so report queued/not-sent requests and do not call those latencies end-to-end queue-inclusive. Abort a failing/overloaded cell with clear evidence; do not keep running forever.

Measure application-side query generation and decoding separately only when relevant; reuse existing integration tests and add custom measurement code only if the native tool cannot answer the question. Capture all measurements as JSON/CSV or equivalent plus a human-readable table. Use query IDs unique to each run and sample; flush query logs after measurement, not within timed samples. Record per-query duration, read_rows, read_bytes, result_rows/bytes, memory_usage, CPU ProfileEvents and spill indicators from supported system.query_log fields. In distributed runs distinguish coordinator/client wall time from worker work; avoid double counting forwarded counters. Record ingestion/replication status, and note missing instrumentation.

## Plans

Capture `EXPLAIN PLAN`, `EXPLAIN indexes = 1`, `EXPLAIN PIPELINE`, and other applicable version-supported variants (including distributed remote plans when applicable). Capture actual measured query execution and ProfileEvents separately; together these show how the query was planned and what it cost to execute. Preserve raw output for baseline/candidate at relevant small/large cells. Check pruning, parts/granules read, join/aggregation/sort work, fanout and memory. Never report that a plan was executed merely because it was explained.

## Public evidence

Reuse the established benchmarking pattern of pinned local containers, separated cache protocols, raw samples, execution plans and before/after tables. Implement against public Core and upstream ClickHouse interfaces. Keep unrelated hosted deployment details out of public artifacts.

## Report

Produce a complete standalone HTML report for every run or audit, including partial or failed runs. Embed readable charts of latency percentiles, throughput and scaling; use separate series for warm and cold samples and small and large workloads. Include an executive summary of observed flaws, ranked recommendations with evidence, a methodology section, and expandable raw execution plans. Provide a link to the finished HTML file and retain the machine-readable samples alongside it. The HTML must render offline without external assets.

Include all matrix rows with status (pass/fail/unrun), data shape, topology, concurrency, cache tier, samples/errors, latency quantiles, throughput, resource counters, baseline/candidate delta and correctness checks. Include reproducible command, exact revision and configuration, and paths to raw plans/samples. Identify the specific bottleneck supported by the data and distinguish poor query shape from hardware/cache/merge effects. State whether further runtime or scale validation is needed; do not extrapolate laptop timings to production SLOs.
