---
name: chbench
description: Benchmark Loco ClickHouse queries and schema changes locally, across shard/replica topologies, tenant sizes and telemetry volumes, with correctness checks, cold/warm tail latency, execution plans and reproducible before/after evidence. Use whenever updating a ClickHouse SQL query (including SQL built in Go) or its schema, or when asked to benchmark Loco’s current ClickHouse implementation, assess scaling, or find performance flaws.
---

# chbench

Measure the actual Loco query and schema, not a simplified stand-in. Run ClickHouse locally in disposable benchmark instances, never against staging, production or customer data. Use the public Core repository, synthetic fixtures and locally generated credentials.

## Before running

1. Read the exact query implementation and its callers in Core. Use the migrations in `internal/clickhouse/migrations/`, the real query builders under `internal/clickhouse/`, and the ingest integration fixtures. Locate the ClickHouse image pin in `charts/loco-control-plane/values.yaml`; do not introduce a second version pin.
2. Read the relevant rules from the [official ClickHouse best-practices skill](https://github.com/ClickHouse/agent-skills/tree/main/skills/clickhouse-best-practices), especially primary/sort keys, index pruning, join cardinality and algorithms, aggregation and text-search semantics. Verify against the pinned version and cite the applicable rules in the report. Do not follow connectivity examples targeting external servers.
3. Read [benchmark protocol](references/protocol.md). Select the query, workload and matrix; keep all applicable dimensions in the final run. A smoke run is not a full benchmark.
4. Use the existing local ClickHouse setup and fixtures (`mise-tasks/test/lib/clickhouse.sh`, `internal/clickhouse/clickhousetest/`).

## Rapid SQL experiments and artifact analysis

Use [clickhouse-local](https://clickhouse.com/docs/concepts/features/tools-and-utilities/clickhouse-local) for optional, serverless SQL experiments before starting a benchmark. Prefer the binary from the pinned server image or a matching local release; check `clickhouse local --version` and record any version mismatch. Use a fresh disposable `--path` per experiment and remove it afterward.

- Reproduce SQL bugs with small deterministic `values()` fixtures, test expressions and aggregates, or use `--queries-file` to run schema and fixture SQL before the query. Preserve the real query's semantics and relevant column types; confirm the result in the Go integration tests on the pinned server.
- Query local benchmark JSONL, CSV or Parquet artifacts directly with `file()` to inspect samples, compare runs and find missing or failed cells. Keep topology, workload, concurrency and cache tier separate when aggregating; use the protocol's percentile definition for reported results and retain raw samples.

For example, inspect a JSONL artifact with fields `variant`, `cache_tier` and `status` (adapt to the actual artifact schema):

```sh
(
  local_data=$(mktemp -d)
  trap 'rm -rf "$local_data"' EXIT
  clickhouse local --path "$local_data" --query "SELECT variant, cache_tier, status, count() FROM file('results.jsonl', JSONEachRow) GROUP BY ALL"
)
```

Local experiments do not replace the pinned server, existing integration tests, or the benchmark matrix. Do not use local-mode timings as server latency or substitute local mode for tests of Keeper, user grants, ingestion, concurrency or replication.

## Run and compare

For a query change, measure before and after. For an audit of the current implementation, benchmark the current ref, identify flaws from the plans and measurements, and rank improvements by impact; a candidate ref is not required.

- Run a 1s1r baseline first to establish query correctness, then the applicable topology and data-scale matrix. Multi-shard testing requires actual distributed routing and verified physical placement, not several independent single-node containers. Check query semantics on every topology.
- Compare baseline and candidate refs using identical seed, data, query arguments, topology, limits and cache protocol. Test both small and large workloads; separate selected tenant size from unrelated tenant volume.
- Use the official `clickhouse-benchmark` for SQL latency, throughput and concurrency. Use existing Go integration tests to verify query results. Write custom tooling only for a requirement the official tool and existing fixtures cannot satisfy.
- Record representative `EXPLAIN` variants and actual execution statistics from query logs; verify cold and warm protocols separately. Include raw samples, errors, timeout counts, p50/p90/p95/p99, throughput, rows/bytes read and memory/CPU where available.
- Do not declare success on a faster but incorrect result, silently dropped rows, failed queries, or a test that only exercised a single topology or small fixture. Record any unrun matrix cells and why; label partial results honestly.
