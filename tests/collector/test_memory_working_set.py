import pytest
from helpers import chart_values, post_otlp, read_output, run_collector

MEBIBYTE = 2**20
WORKING_SET_UTILIZATION = "k8s.pod.memory.working_set_limit_utilization"


def gauge(name, value):
    return {
        "name": name,
        "gauge": {
            "dataPoints": [{"asInt" if isinstance(value, int) else "asDouble": value}]
        },
    }


def test_working_set_is_rated_against_the_pod_memory_limit(tmp_path):
    daemon = chart_values("loco-worker")["nodeCollector"]["config"]
    (pipeline,) = [
        p
        for p in daemon["service"]["pipelines"].values()
        if p["receivers"] == ["kubeletstats"]
    ]
    generators = [
        p for p in pipeline["processors"] if p.startswith("metricsgeneration")
    ]
    config = {
        "receivers": {"otlp": {"protocols": {"http": {"endpoint": "0.0.0.0:4318"}}}},
        "processors": {name: daemon["processors"][name] for name in generators},
        "exporters": {"file": {"path": "/data/output.json", "flush_interval": "100ms"}},
        "service": {
            "pipelines": {
                "metrics": {
                    "receivers": ["otlp"],
                    "processors": generators,
                    "exporters": ["file"],
                }
            }
        },
    }
    pods = {"cached": (64, 30, 13), "idle": (128, 20, 20)}
    with run_collector(tmp_path, config, "node-collector-agent") as collector:
        resources = [
            {
                "resource": {
                    "attributes": [
                        {"key": "k8s.pod.name", "value": {"stringValue": pod}}
                    ]
                },
                "scopeMetrics": [
                    {
                        "metrics": [
                            gauge("k8s.pod.memory.usage", usage * MEBIBYTE),
                            gauge("k8s.pod.memory_limit_utilization", usage / limit),
                            gauge("k8s.pod.memory.working_set", working_set * MEBIBYTE),
                        ]
                    }
                ],
            }
            for pod, (limit, usage, working_set) in pods.items()
        ]
        post_otlp(collector, "metrics", {"resourceMetrics": resources})
        received = read_output(
            collector,
            lambda document: [
                (
                    resource["resource"]["attributes"][0]["value"]["stringValue"],
                    metric["gauge"]["dataPoints"][0]["asDouble"],
                )
                for resource in document["resourceMetrics"]
                for scope in resource["scopeMetrics"]
                for metric in scope["metrics"]
                if metric["name"] == WORKING_SET_UTILIZATION
            ],
            len(pods),
        )
    for pod, ratio in received:
        limit, _, working_set = pods[pod]
        assert ratio == pytest.approx(working_set / limit), pod
