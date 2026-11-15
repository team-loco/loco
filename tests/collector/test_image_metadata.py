def test_workload_enrichment_collects_stable_image_metadata(worker_relays):
    for collector, config in worker_relays.items():
        enrichers = []
        for name, processor in config["processors"].items():
            if not name.startswith("k8s_attributes/"):
                continue
            metadata = processor.get("extract", {}).get("metadata", [])
            if "k8s.container.name" not in metadata:
                continue
            enrichers.append(name)
            assert {
                "container.image.name",
                "container.image.tags",
                "container.image.repo_digests",
            } <= set(metadata), (collector, name)
        assert enrichers, collector
