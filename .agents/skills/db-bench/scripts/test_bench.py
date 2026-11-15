import datetime

import pytest

import bench


def test_parse_queries_splits_on_sqlc_names():
    text = "-- name: A :one\nSELECT 1;\n\n-- name: B :many\nSELECT 2\nFROM t;\n"
    assert bench.parse_queries(text) == {"A": "SELECT 1", "B": "SELECT 2\nFROM t"}


def test_bind_numbers_named_args_after_positional_ones_in_order_of_first_use():
    sql, keys = bench.bind(
        "SELECT sqlc.embed(e) FROM e WHERE a = $1 AND b = sqlc.arg('b') AND (sqlc.narg(c)::int IS NULL OR c = sqlc.narg(c)) AND d = sqlc.arg('b') LIMIT $2"
    )
    assert sql == "SELECT e.* FROM e WHERE a = $1 AND b = $3 AND ($4::int IS NULL OR c = $4) AND d = $3 LIMIT $2"
    assert keys == [1, 2, "b", "c"]


def test_every_api_query_binds_to_contiguous_parameters():
    queries = bench.Source(None).queries()
    assert "DeleteEventsBefore" in queries
    for name, sql in queries.items():
        bound, keys = bench.bind(sql)
        assert "sqlc." not in bound, name
        assert sorted({int(n) for n in bench.POSITIONAL_RE.findall(bound)}) == list(range(1, len(keys) + 1)), name


def test_every_scenario_query_exists():
    queries = bench.Source(None).queries()
    for scenario in bench.SCENARIOS.values():
        for statements in scenario.cases(2, bench.Source(None)).values():
            for query, params in statements:
                assert query in queries, (scenario.name, query)
                missing = [k for k in bench.bind(queries[query])[1] if k not in params]
                assert not missing, (scenario.name, query, missing)


def test_go_const_reads_ints_and_durations():
    text = "const (\n\tdefaultPageSize = 50\n\tpoll = 2 * time.Second\n\twait = 250 * time.Millisecond\n)\n"
    assert bench.go_const(text, "defaultPageSize") == 50
    assert bench.go_const(text, "poll") == 2
    assert bench.go_const(text, "wait") == 0.25
    with pytest.raises(bench.BenchError):
        bench.go_const(text, "missing")


def test_encode_sends_text_values():
    assert bench.encode(None) is None
    assert bench.encode(True) == b"true"
    assert bench.encode(7) == b"7"
    assert bench.encode(["a", 'b"c']) == b'{"a","b\\"c"}'
    assert bench.encode([]) == b"{}"
    stamp = datetime.datetime(2026, 1, 2, tzinfo=datetime.UTC)
    assert bench.encode(stamp) == b"2026-01-02T00:00:00+00:00"


def test_goose_up_drops_the_down_section():
    assert bench.goose_up("-- +goose Up\nCREATE TABLE t ();\n-- +goose Down\nDROP TABLE t;\n").strip() == "CREATE TABLE t ();"


def test_plan_summary_names_the_scan_that_examined_most_rows():
    plan = [{
        "Execution Time": 10,
        "Plan": {"Node Type": "Limit", "Plans": [
            {"Node Type": "Index Scan", "Relation Name": "users", "Index Name": "users_pkey", "Actual Rows": 1, "Actual Loops": 50},
            {"Node Type": "Seq Scan", "Relation Name": "events", "Actual Rows": 1, "Rows Removed by Filter": 900, "Actual Loops": 1},
        ]},
    }]
    assert bench.plan_summary(plan) == "Seq Scan on events"


def test_plan_summary_names_the_slowest_trigger_when_triggers_dominate():
    plan = [{
        "Execution Time": 10,
        "Plan": {"Node Type": "ModifyTable", "Plans": [{"Node Type": "Index Scan", "Relation Name": "users", "Actual Rows": 1}]},
        "Triggers": [
            {"Trigger Name": "RI_1", "Constraint Name": "invitations_invited_by_fkey", "Time": 4, "Calls": 1},
            {"Trigger Name": "RI_2", "Constraint Name": "invitations_claimed_by_fkey", "Time": 3, "Calls": 1},
        ],
    }]
    assert bench.plan_summary(plan) == "invitations_invited_by_fkey (1 calls)"


def test_percentile_uses_nearest_rank():
    assert bench.percentile(list(range(1, 21)), 0.95) == 19
    assert bench.percentile([5], 0.95) == 5


def test_markdown_puts_before_and_after_in_one_cell():
    scenario = bench.SCENARIOS["delete-secrets"]
    ok = {"status": "ok", "p50_ms": 1.0, "p95_ms": 2.0, "plan_ms": 0.0, "cpu_ms": 1.0, "shared_hit": 5,
          "shared_read": 0, "temp_blocks": 0, "wal_records": 1, "anon_mb": 30, "plan": "Seq Scan on secrets"}
    results = [
        {"ref": "main", "size": 1000, "case": "names"} | ok,
        {"ref": "fix", "size": 1000, "case": "names"} | ok | {"p50_ms": 0.5, "plan": "Index Scan on secrets"},
        {"ref": "main", "size": 1_000_000, "case": "names", "status": "timeout"},
    ]
    table = bench.markdown(scenario, results, ["main", "fix"]).splitlines()
    assert table[-2].startswith("| 1k | names | 1 → 0.5 | 2 | ")
    assert "Seq Scan on secrets → Index Scan on secrets" in table[-2]
    assert table[-1].startswith("| 1M | names | timeout → – |")
