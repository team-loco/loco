---
name: pgbench
description: Benchmark Loco PostgreSQL queries, transactions and schema changes locally across tenant sizes, database topologies and concurrent workloads. Use whenever updating PostgreSQL SQL (including SQL built in Go), indexes or schema, or when asked to benchmark the current PostgreSQL implementation, assess scaling or find performance flaws.
---

# pgbench

Run PostgreSQL locally on disposable instances with synthetic data and credentials. Measure the real Loco queries and transaction boundaries. The goal is accurate results, simple SQL with low planning and execution costs, and no performance regressions across supported workload sizes.

1. Read the affected queries in `api/queries/`, SQL built in Go, their callers and the migrations in `api/migrations/`. Use the PostgreSQL image pinned in `charts/loco-control-plane/values.yaml`, the CloudNativePG configuration in the chart and the application pool settings in `api/db/db.go`.
2. Read the [benchmark protocol](references/protocol.md).
3. Use PostgreSQL's native [pgbench](https://www.postgresql.org/docs/18/pgbench.html) with custom scripts for SQL and transaction latency, throughput and concurrency. Reuse `api/db/dbtest/` and existing database integration tests for correctness. Write custom tooling only when these cannot answer the question.
4. For changes, compare before and after under matched conditions. For audits, measure the current revision and rank flaws by observed impact. Test the data shapes and workload matrix in the protocol; evaluate planning cost as well as execution cost. Simplify expensive queries and verify the simpler SQL against the same matrix.
