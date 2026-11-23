import { create } from "@bufbuild/protobuf";
import { describe, expect, test } from "bun:test";
import { MetricSeriesSchema } from "@gen/loco/observability/v1/observability_pb";
import { DeploymentSchema, ReplicaSchema } from "@gen/loco/deployment/v1/deployment_pb";

import { msToTimestamp } from "@/lib/time";

import type { RegionView } from "./model";
import { podUsage, replicaLimits } from "./replicas";

const NOW_MS = Date.UTC(2026, 9, 11, 12, 0, 0);
const MINUTE_MS = 60_000;

describe("podUsage", () => {
	const series = (pod: string | undefined, points: [number, number][]) =>
		create(MetricSeriesSchema, {
			resourceId: "r",
			labels: pod === undefined ? {} : { "k8s.pod.name": pod },
			points: points.map(([agoMs, value]) => ({ timestamp: msToTimestamp(NOW_MS - agoMs), value })),
		});

	test("takes each pod's newest value within two collection intervals", () => {
		const usage = podUsage(
			[
				series("web-a", [
					[30_000, 0.4],
					[5 * MINUTE_MS, 0.9],
				]),
				series("web-b", [[3 * MINUTE_MS, 0.7]]),
				series(undefined, [[10_000, 0.5]]),
			],
			NOW_MS,
		);
		expect(usage.get("web-a")).toBe(0.4);
		expect(usage.has("web-b")).toBe(false);
		expect(usage.size).toBe(1);
	});
});

describe("replicaLimits", () => {
	const sized = (id: string, cpu: string, memory: string) =>
		create(DeploymentSchema, {
			id,
			spec: { spec: { case: "service", value: { regions: { "us-ca-1": { cpu, memory } } } } },
		});
	const resized = sized("new", "1", "2Gi");
	const previous = sized("old", "500m", "256Mi");
	const region: RegionView = {
		name: "us-ca-1",
		status: undefined,
		current: resized,
		serving: previous,
		lastGood: previous,
		history: [resized, previous],
	};

	test("takes each replica's limits from the deployment it runs during a resize", () => {
		const old = create(ReplicaSchema, { name: "web-old", deploymentId: "old" });
		const fresh = create(ReplicaSchema, { name: "web-new", deploymentId: "new" });
		expect(replicaLimits(region, old)).toEqual({ cpuMilli: 500, memoryMi: 256 });
		expect(replicaLimits(region, fresh)).toEqual({ cpuMilli: 1000, memoryMi: 2048 });
	});

	test("falls back to the current deployment for a replica of an unknown one", () => {
		const unknown = create(ReplicaSchema, { name: "web-x", deploymentId: "gone" });
		expect(replicaLimits(region, unknown)).toEqual({ cpuMilli: 1000, memoryMi: 2048 });
	});
});
