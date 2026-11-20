import json

from helpers import read_output, run_collector

POD_LOGS = "/var/log/pods"
TEST_LOGS = "/data/pods"
TIMESTAMP = "2026-10-19T12:00:00.000000000Z"


def file_log_pipeline(relays):
    """The node collector's file-log receiver and processors, reading pod logs from /data/pods."""
    config = relays["otel-col-daemon-agent"]
    receiver = json.loads(
        json.dumps(config["receivers"]["file_log"]).replace(POD_LOGS, TEST_LOGS)
    )
    receiver.pop("storage")
    receiver["start_at"] = "beginning"
    processors = [
        name
        for name in config["service"]["pipelines"]["logs"]["processors"]
        if not name.startswith(("memory_limiter", "k8s_attributes"))
    ]
    return {
        "receivers": {"file_log": receiver},
        "processors": {name: config["processors"][name] for name in processors},
        "exporters": {"file": {"path": "/data/output.json", "flush_interval": "100ms"}},
        "service": {
            "pipelines": {
                "logs": {
                    "receivers": ["file_log"],
                    "processors": processors,
                    "exporters": ["file"],
                }
            }
        },
    }


def write_pod_log(directory, namespace, lines, pod="app", container="app"):
    """Write lines to a container log file; a (stream, line) pair sets the line's stream, which is stdout otherwise."""
    path = (
        directory
        / "pods"
        / f"{namespace}_{pod}_00000000-0000-4000-8000-000000000001"
        / container
        / "0.log"
    )
    path.parent.mkdir(parents=True)
    entries = (line if isinstance(line, tuple) else ("stdout", line) for line in lines)
    path.write_text(
        "".join(f"{TIMESTAMP} {stream} F {line}\n" for stream, line in entries)
    )


def collected_records(collector, count):
    return read_output(
        collector,
        lambda document: [
            record
            for resource in document["resourceLogs"]
            for scope in resource["scopeLogs"]
            for record in scope["logRecords"]
        ],
        count,
    )


def body(record):
    return record["body"]["stringValue"]


def stream(record):
    return next(
        attribute["value"]["stringValue"]
        for attribute in record["attributes"]
        if attribute["key"] == "log.iostream"
    )


def test_platform_namespace_logs_stay_on_the_node(worker_relays, tmp_path):
    for namespace in (
        "kube-system",
        "flux-system",
        "cert-manager",
        "loco-system",
        "loco-obs",
        "loco-ws-a",
    ):
        write_pod_log(tmp_path, namespace, [f"from {namespace}"])
    write_pod_log(
        tmp_path,
        "loco-obs-node",
        ["from the collector"],
        "otel-col-daemon-x",
        "otel-col-daemon",
    )
    config = file_log_pipeline(worker_relays)
    with run_collector(tmp_path, config, "otel-col-daemon") as collector:
        assert sorted(map(body, collected_records(collector, 3))) == [
            "from loco-obs",
            "from loco-system",
            "from loco-ws-a",
        ]


GO_PANIC = [
    "panic: runtime error: invalid memory address or nil pointer dereference",
    "[signal SIGSEGV: segmentation violation code=0x1 addr=0x0 pc=0x8b1c2a]",
    "goroutine 210 [running]:",
    "main.(*checkout).PlaceOrder(0xc0001a2000, {0x130ada0, 0xc000312000})",
    "\t/usr/src/app/main.go:259 +0x2a",
    "panic({0x130ada0?, 0x1f4b3c0?})",
    "\t/usr/local/go/src/runtime/panic.go:859 +0x13a",
    "created by net/http.(*Server).Serve in goroutine 1",
    "\t/usr/local/go/src/net/http/server.go:3285 +0x4b4",
]


def test_multiline_entries_arrive_as_one_record(worker_relays, tmp_path):
    lines = [
        "starting checkout",
        GO_PANIC[0],
        GO_PANIC[1],
        "",
        *GO_PANIC[2:],
        "info: cart.cartstore.ValkeyCartStore[1662118147]",
        "      GetCartAsync called with userId=42",
        "java.lang.IllegalStateException: closed",
        "\tat shop.Ads.serve(Ads.java:12)",
        "Caused by: java.io.IOException: reset",
        "\t... 3 more",
        "GET /health 200",
        "  ",
        "GET /health 200",
        "user.login(alice)",
    ]
    write_pod_log(tmp_path, "loco-ws-a", lines)
    expected = [
        ("starting checkout", ""),
        ("\n".join(GO_PANIC), "FATAL"),
        (
            "info: cart.cartstore.ValkeyCartStore[1662118147]\n      GetCartAsync called with userId=42",
            "INFO",
        ),
        (
            "java.lang.IllegalStateException: closed\n\tat shop.Ads.serve(Ads.java:12)\n"
            "Caused by: java.io.IOException: reset\n\t... 3 more",
            "",
        ),
        ("GET /health 200", ""),
        ("GET /health 200", ""),
        ("user.login(alice)", ""),
    ]
    with run_collector(
        tmp_path, file_log_pipeline(worker_relays), "otel-col-daemon"
    ) as collector:
        records = collected_records(collector, len(expected))
    assert [
        (body(record), record.get("severityText", "")) for record in records
    ] == expected


def test_multiline_entries_join_only_lines_of_their_stream(worker_relays, tmp_path):
    lines = [
        ("stderr", GO_PANIC[0]),
        ("stderr", GO_PANIC[1]),
        ("stdout", "GET /health 200"),
        *(("stderr", line) for line in GO_PANIC[2:]),
        ("stdout", "GET /ready 200"),
    ]
    write_pod_log(tmp_path, "loco-ws-a", lines)
    with run_collector(
        tmp_path, file_log_pipeline(worker_relays), "otel-col-daemon"
    ) as collector:
        records = collected_records(collector, 3)
    assert sorted(
        (stream(record), body(record), record.get("severityText", ""))
        for record in records
    ) == [
        ("stderr", "\n".join(GO_PANIC), "FATAL"),
        ("stdout", "GET /health 200", ""),
        ("stdout", "GET /ready 200", ""),
    ]


HTTP_ECHO_LINES = [
    ("stderr", "2026/10/19 12:00:00 [INFO] server is listening on :5678"),
    '2026/10/19 12:00:01 web.loco.test 10.244.0.7:51234 "GET / HTTP/1.1" 200 6 "curl/8.5.0" 41.25µs',
    "",
    '2026/10/19 12:00:02 web.loco.test 10.244.0.7:51240 "GET /health HTTP/1.1" 200 6 "kube-probe/1.33" 12.5µs',
    "",
    "",
    '2026/10/19 12:00:03 web.loco.test 10.244.0.7:51252 "GET /ready HTTP/1.1" 200 6 "kube-probe/1.33" 9.75µs',
]


def test_http_echo_access_lines_leave_no_empty_records(worker_relays, tmp_path):
    write_pod_log(tmp_path, "loco-ws-a", HTTP_ECHO_LINES)
    texts = (line[1] if isinstance(line, tuple) else line for line in HTTP_ECHO_LINES)
    expected = sorted(text for text in texts if text)
    with run_collector(
        tmp_path, file_log_pipeline(worker_relays), "otel-col-daemon"
    ) as collector:
        records = collected_records(collector, len(expected))
    assert sorted(map(body, records)) == expected
