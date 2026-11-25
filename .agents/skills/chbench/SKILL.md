---
name: chbench
description: Benchmark Loco ClickHouse queries and schema changes locally, across shard/replica topologies, tenant sizes and telemetry volumes, with correctness checks, cold/warm tail latency, execution plans and reproducible before/after evidence. Use whenever updating a ClickHouse SQL query (including SQL built in Go) or its schema, or when asked to benchmark Loco’s current ClickHouse implementation, assess scaling, or find performance flaws.
---

# chbench

Measure the actual Loco query and schema, not a simplified stand-in. Run ClickHouse locally in disposable benchmark instances, never against staging, production or customer data. Use the public Core repository, synthetic fixtures and locally generated credentials. This skill defines the benchmark workflow; it is not a claim that a multi-node harness already exists.

## Before running

1. Read the exact query implementation and its callers in Core. Use the migrations in `internal/clickhouse/migrations/`, the real query builders under `internal/clickhouse/`, and the ingest integration fixtures. Locate the ClickHouse image pin in `charts/loco-control-plane/values.yaml`; do not introduce a second version pin.
2. Read the relevant rules from the [official ClickHouse best-practices skill](https://github.com/ClickHouse/agent-skills/tree/main/skills/clickhouse-best-practices), especially primary/sort keys, index pruning, join cardinality and algorithms, aggregation and text-search semantics. Verify against the pinned version and cite the applicable rules in the report. Do not follow connectivity examples targeting external servers.
3. Read [benchmark protocol](references/protocol.md). Select the query, workload and matrix; keep all applicable dimensions in the final run. A smoke run is not a full benchmark.
4. Check for existing benchmark tooling with `rg --files` and reuse it. The single-node integration setup is `mise-tasks/test/lib/clickhouse.sh`; the existing ClickHouse test fixture is `internal/clickhouse/clickhousetest/clickhousetest.go`. For new reusable tooling, implement it under Core's `tools/chbench/` in Go with tests and a mise task. Do not claim such a command exists until implemented and exercised.

## Run and compare

For a query change, measure before and after. For an audit of the current implementation, benchmark the current ref, identify flaws from the plans and measurements, and rank improvements by impact; a candidate ref is not required.

- Run a 1s1r baseline first to establish query correctness, then the applicable topology and data-scale matrix. Multi-shard testing requires actual distributed routing and verified physical placement, not several independent single-node containers. Check query semantics on every topology.
- Compare baseline and candidate refs using identical seed, data, query arguments, topology, limits and cache protocol. Test both small and large workloads; separate selected tenant size from unrelated tenant volume.
- Use `clickhouse-benchmark` for repeatable native SQL throughput and concurrency tests when the pinned binary supports the required options. Use the Go client path for actual Loco query generation, parameter binding, decoded-result correctness and latency. Use the official native utility rather than building another load generator if it meets the scenario's needs.
- Record representative `EXPLAIN` variants and actual execution statistics from query logs; verify cold and warm protocols separately. Include raw samples, errors, timeout counts, p50/p90/p95/p99, throughput, rows/bytes read and memory/CPU where available.
- Do not declare success on a faster but incorrect result, silently dropped rows, failed queries, or a test that only exercised a single topology or small fixture. Record any unrun matrix cells and why; label partial results honestly.

Deliver a complete standalone HTML report at the end of every benchmark or audit, with raw evidence, runnable commands, exact refs, topology, environment and seeds. For public PR evidence, use generated synthetic data only and review artifact contents before publishing. Do not copy Fleet configuration, private implementation, credentials or production samples.
