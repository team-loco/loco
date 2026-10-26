import { create } from "@bufbuild/protobuf";
import { describe, expect, test } from "bun:test";
import {
	EnvironmentStatusSchema,
	RegionStatusSchema,
	ResourceStatus,
	type EnvironmentStatus,
	type RegionStatus,
} from "@gen/loco/resource/v1/resource_pb";

import { msToTimestamp } from "@/lib/time";

import { replicaLine } from "./replicas";

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

function environment(...regions: RegionStatus[]): EnvironmentStatus {
	return create(EnvironmentStatusSchema, { regions });
}

describe("replicaLine", () => {
	test("says not deployed without an environment status", () => {
		expect(replicaLine(undefined, NOW_MS)).toBe("Not deployed");
	});

	test("shows the live count while every region reports", () => {
		const env = environment(region("us-east-1", ResourceStatus.HEALTHY, 2, 2, 1));
		expect(replicaLine(env, NOW_MS)).toBe("2/2 replicas ready");
	});

	test("counts across regions", () => {
		const env = environment(
			region("us-east-1", ResourceStatus.HEALTHY, 2, 2, 1),
			region("eu-west-1", ResourceStatus.DEGRADED, 1, 2, 1),
		);
		expect(replicaLine(env, NOW_MS)).toBe("3/4 replicas ready · 2 regions");
	});

	test("marks the count as the last report when no region reports", () => {
		const env = environment(region("us-east-1", ResourceStatus.UNKNOWN, 2, 2, 5));
		expect(replicaLine(env, NOW_MS)).toBe("2/2 replicas ready at last report, 5 min ago");
	});

	test("uses the oldest report across silent regions", () => {
		const env = environment(
			region("us-east-1", ResourceStatus.UNKNOWN, 2, 2, 5),
			region("eu-west-1", ResourceStatus.UNKNOWN, 1, 1, 180),
		);
		expect(replicaLine(env, NOW_MS)).toBe("3/3 replicas ready at last report, 3 hours ago · 2 regions");
	});

	test("does not claim a count that was never reported", () => {
		const env = environment(region("us-east-1", ResourceStatus.UNKNOWN, 0, 1));
		expect(replicaLine(env, NOW_MS)).toBe("Replicas not reported yet");
	});

	test("names the silent region when others still report", () => {
		const env = environment(
			region("us-east-1", ResourceStatus.HEALTHY, 2, 2, 1),
			region("eu-west-1", ResourceStatus.UNKNOWN, 1, 1, 12),
		);
		expect(replicaLine(env, NOW_MS)).toBe("3/3 replicas ready · 2 regions · eu-west-1 last reported 12 min ago");
	});

	test("names a silent region that never reported", () => {
		const env = environment(
			region("us-east-1", ResourceStatus.HEALTHY, 2, 2, 1),
			region("eu-west-1", ResourceStatus.UNKNOWN, 0, 1),
		);
		expect(replicaLine(env, NOW_MS)).toBe("2/3 replicas ready · 2 regions · eu-west-1 never reported");
	});
});
