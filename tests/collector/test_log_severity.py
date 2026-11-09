import json
import subprocess

import pytest

from helpers import ROOT, run_collector


def chart_value(expression):
    return json.loads(subprocess.check_output(
        ["yq", "-o=json", expression, str(ROOT / "charts/loco-worker/values.yaml")], text=True
    ))


@pytest.mark.parametrize("source", ["otlp", "file_log"])
def test_collected_log_severity(tmp_path, source):
    transform = chart_value('.["otel-col-daemon"].config.processors."transform/log_severity"')
    cases = [
        ('{"level":"warning","message":"low memory"}', "WARN", 13, {}),
        ('{"severity":"error","message":"failed"}', "ERROR", 17, {}),
        ('{"severityText":"Debug","message":"checking"}', "DEBUG", 5, {}),
        ("2026-10-19T12:00:00Z WARNING low memory", "WARN", 13, {}),
        ("[ERROR] request failed", "ERROR", 17, {}),
        ("\x1b[33mWARN\x1b[0m low memory", "WARN", 13, {}),
        ("level=warn msg=slow", "WARN", 13, {}),
        ('time=2026-10-19T12:00:00Z level="WARN" msg=slow', "WARN", 13, {}),
        ('  {"level":"warning"}', "WARN", 13, {}),
        ("INFO normal", "INFO", 9, {}),
        ("WARNING", "WARN", 13, {}),
        ('msg="request includes level=error token"', "", 0, {}),
        ("TRACE detail", "TRACE", 1, {}),
        ('{"level":"critical"}', "FATAL", 21, {}),
        ("a message mentioning warning", "", 0, {}),
        ('{"level":', "", 0, {}),
        ('{"level":123}', "", 0, {}),
        ('{"level":"custom"}', "", 0, {}),
        ('{"message":"request includes level=error token"}', "", 0, {}),
        ('{"message":"level=warn unfinished', "", 0, {}),
        ("WARNING preserved SDK error", "ERROR", 17, {"severityText": "ERROR", "severityNumber": 17}),
        ("WARNING preserved custom text", "custom", 0, {"severityText": "custom"}),
        ("WARNING preserved number", "", 17, {"severityNumber": 17}),
    ]
    config = {
        "receivers": {"otlp": {"protocols": {"http": {"endpoint": "0.0.0.0:4318"}}}},
        "processors": {"transform/log_severity": transform},
        "exporters": {"file": {"path": "/data/output.json", "flush_interval": "100ms"}},
        "service": {"pipelines": {"logs": {"receivers": ["otlp"], "processors": ["transform/log_severity"], "exporters": ["file"]}}},
    }
    if source == "file_log":
        cases = [case for case in cases if not case[3]]
        log_path = tmp_path / "pods/default_fixture_00000000-0000-4000-8000-000000000001/app/0.log"
        log_path.parent.mkdir(parents=True)
        log_path.write_text("".join(f"2026-10-19T12:00:00.000000000Z stdout F {body}\n" for body, _, _, _ in cases))
        config["receivers"] = {"file_log": {"include": ["/data/pods/*/*/*.log"], "include_file_path": True, "start_at": "beginning", "operators": [{"id": "container-parser", "type": "container"}]}}
        config["service"]["pipelines"]["logs"]["receivers"] = ["file_log"]
    records = [{"body": {"stringValue": body}, "attributes": [{"key": "case", "value": {"intValue": str(i)}}], **initial}
               for i, (body, _, _, initial) in enumerate(cases)]
    payload = {"resourceLogs": [{"scopeLogs": [{"logRecords": records}]}]} if source == "otlp" else None
    received = [record for _, record in run_collector(tmp_path, config, len(cases), payload)]
    for record, (body, severity, number, _) in zip(received, cases, strict=True):
        assert record["body"]["stringValue"] == body
        assert record.get("severityText", "") == severity, record
        assert record.get("severityNumber", 0) == number, record
