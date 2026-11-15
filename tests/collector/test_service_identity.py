"""Application service identity is not inferred from infrastructure names."""

SERVICE_IDENTITY = {
    "service.name",
    "service.namespace",
    "service.version",
    "service.instance.id",
}


def test_kubernetes_enrichment_leaves_service_identity_to_applications(worker_relays):
    for collector, config in worker_relays.items():
        for name, processor in config["processors"].items():
            if not name.startswith("k8s_attributes/"):
                continue
            metadata = set(processor["extract"]["metadata"])
            assert metadata.isdisjoint(SERVICE_IDENTITY), (collector, name, metadata)
            if name != "k8s_attributes/route_namespace":
                assert {"k8s.namespace.name", "k8s.pod.name", "k8s.pod.uid"} <= metadata
