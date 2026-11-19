import { create } from "@bufbuild/protobuf";
import { describe, expect, test } from "bun:test";
import {
	EnvironmentStatusSchema,
	RegionStatusSchema,
	ResourceStatus,
	type EnvironmentStatus,
	type RegionStatus,
} from "@gen/loco/resource/v1/resource_pb";

import { MetricSeriesSchema } from "@gen/loco/observability/v1/observability_pb";
import { ReplicaExitSchema, ReplicaRestartSchema, ReplicaSchema } from "@gen/loco/deployment/v1/deployment_pb";

import { msToTimestamp } from "@/lib/time";

import { exitLabel, podUsage, replicaLine, restartLabel } from "./replicas";

const NOW_MS = Date.UTC(2026, 9, 11, 12, 0, 0);
const MINUTE_MS = 60_000;

function region(
	name: string,
	status: ResourceStatus,
	ready: number,
	desired: number,
	reportedMinutesAgo?: number,
): RegionStatus {
	return create(RegionStatusSchema, {
		region: name,
		status,
		readyReplicas: ready,
		desiredReplicas: desired,
		...(reportedMinutesAgo === undefined ? {} : { reportedAt: msToTimestamp(NOW_MS - reportedMinutesAgo * MINUTE_MS) }),
	});
}

const statusCount = (r: RegionStatus) => ({ ready: r.readyReplicas, desired: r.desiredReplicas });

function environment(...regions: RegionStatus[]): EnvironmentStatus {
	return create(EnvironmentStatusSchema, { regions });
}

describe("replicaLine", () => {
	test("says not deployed without an environment status", () => {
		expect(replicaLine(undefined, NOW_MS, statusCount)).toBe("Not deployed");
	});

	test("shows the live count while every region reports", () => {
		const env = environment(region("us-east-1", ResourceStatus.HEALTHY, 2, 2, 1));
		expect(replicaLine(env, NOW_MS, statusCount)).toBe("2 of 2 ready");
	});

	test("counts across regions", () => {
		const env = environment(
			region("us-east-1", ResourceStatus.HEALTHY, 2, 2, 1),
			region("eu-west-1", ResourceStatus.DEGRADED, 1, 2, 1),
		);
		expect(replicaLine(env, NOW_MS, statusCount)).toBe("3 of 4 ready · 2 regions");
	});

	test("marks the count as the last report when no region reports", () => {
		const env = environment(region("us-east-1", ResourceStatus.UNKNOWN, 2, 2, 5));
		expect(replicaLine(env, NOW_MS, statusCount)).toBe("2 of 2 ready at last report, 5m ago");
	});

	test("uses the oldest report across silent regions", () => {
		const env = environment(
			region("us-east-1", ResourceStatus.UNKNOWN, 2, 2, 5),
			region("eu-west-1", ResourceStatus.UNKNOWN, 1, 1, 180),
		);
		expect(replicaLine(env, NOW_MS, statusCount)).toBe("3 of 3 ready at last report, 3h ago · 2 regions");
	});

	test("does not claim a count that was never reported", () => {
		const env = environment(region("us-east-1", ResourceStatus.UNKNOWN, 0, 1));
		expect(replicaLine(env, NOW_MS, statusCount)).toBe("Replicas not reported yet");
	});

	test("names the silent region when others still report", () => {
		const env = environment(
			region("us-east-1", ResourceStatus.HEALTHY, 2, 2, 1),
			region("eu-west-1", ResourceStatus.UNKNOWN, 1, 1, 12),
		);
		expect(replicaLine(env, NOW_MS, statusCount)).toBe("3 of 3 ready · 2 regions · eu-west-1 last reported 12m ago");
	});

	test("names a silent region that never reported", () => {
		const env = environment(
			region("us-east-1", ResourceStatus.HEALTHY, 2, 2, 1),
			region("eu-west-1", ResourceStatus.UNKNOWN, 0, 1),
		);
		expect(replicaLine(env, NOW_MS, statusCount)).toBe("2 of 3 ready · 2 regions · eu-west-1 never reported");
	});
});

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

describe("exitLabel", () => {
	test("names the reason, exit code and how long ago the container exited", () => {
		const exit = create(ReplicaExitSchema, {
			reason: "OOMKilled",
			exitCode: 137,
			finishedAt: msToTimestamp(NOW_MS - 5 * MINUTE_MS),
		});
		expect(exitLabel(exit, NOW_MS)).toBe("OOMKilled · exit 137 · 5m ago");
		expect(exitLabel(create(ReplicaExitSchema, { reason: "Error", exitCode: 1 }), NOW_MS)).toBe("Error · exit 1");
	});
});

describe("restartLabel", () => {
	const restarted = (restarts: number, cause: string, exitCode = 1) =>
		create(ReplicaSchema, {
			restarts,
			lastRestart: create(ReplicaRestartSchema, { at: msToTimestamp(NOW_MS - 2 * MINUTE_MS), cause, exitCode }),
		});

	test("counts restarts and names the last one's time and cause", () => {
		expect(restartLabel(restarted(3, "exit"), NOW_MS)).toBe("3 restarts · last 2m ago · exit 1");
		expect(restartLabel(restarted(1, "health_check", 137), NOW_MS)).toBe("1 restart · last 2m ago · health check");
		expect(restartLabel(restarted(2, "out_of_memory", 137), NOW_MS)).toBe("2 restarts · last 2m ago · out of memory");
	});

	test("shows only the count before any restart", () => {
		expect(restartLabel(create(ReplicaSchema, { restarts: 0 }), NOW_MS)).toBe("0 restarts");
	});
});
