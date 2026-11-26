import { describe, expect, test } from "bun:test";
import { create } from "@bufbuild/protobuf";
import { timestampFromMs } from "@bufbuild/protobuf/wkt";
import { ClusterCapacitySchema, ClusterSchema } from "@gen/loco/cluster/v1/cluster_pb";

import { availableCapacity, headroom, reservations } from "./capacity-model";

const capacity = () =>
	create(ClusterCapacitySchema, {
		cpuMillicoresAllocatable: 8000n,
		cpuMillicoresRequested: 3500n,
		memoryBytesAllocatable: 16n * 1024n ** 3n,
		memoryBytesRequested: 6n * 1024n ** 3n,
		podsAllocatable: 220,
		podsAssigned: 42,
		podsRunning: 40,
		reportedAt: timestampFromMs(1000),
	});

const cluster = () =>
	create(ClusterSchema, {
		isActive: true,
		healthStatus: "healthy",
		lastHeartbeat: timestampFromMs(1000),
		capacity: capacity(),
	});

describe("deployment headroom", () => {
	test("separates requested CPU/memory and assigned pod slots from measured usage", () => {
		const rows = reservations(capacity());
		expect(rows.map(headroom)).toEqual(["4.5 vCPU unreserved", "10 GiB unreserved", "178 unreserved"]);
		expect(rows[2]?.requested).toBe(42);
	});

	test("reports oversubscription instead of hiding it as zero free", () => {
		const report = capacity();
		report.cpuMillicoresRequested = 9000n;
		expect(headroom(reservations(report)[0]!)).toBe("1 vCPU over capacity");
	});

	test("excludes unhealthy, silent, paused, removed, absent and stale reports from the pool", () => {
		const unhealthy = cluster();
		unhealthy.healthStatus = "unhealthy";
		const paused = cluster();
		paused.isActive = false;
		const removed = cluster();
		removed.removedAt = timestampFromMs(1000);
		const silent = cluster();
		silent.silentSince = timestampFromMs(1000);
		const stale = cluster();
		stale.capacity!.stale = true;
		const absent = cluster();
		absent.capacity = undefined;
		const result = availableCapacity([cluster(), cluster(), unhealthy, paused, removed, silent, stale, absent]);
		expect(result.included).toBe(2);
		expect(result.capacity?.cpuMillicoresAllocatable).toBe(16000n);
		expect(result.capacity?.cpuMillicoresRequested).toBe(7000n);
	});

	test("dates pooled headroom by the oldest included report", () => {
		const newest = cluster();
		newest.capacity!.reportedAt = timestampFromMs(9000);
		const oldest = cluster();
		oldest.capacity!.reportedAt = timestampFromMs(3000);
		expect(availableCapacity([newest, oldest]).capacity?.reportedAt).toEqual(timestampFromMs(3000));
	});

	test("shows unavailable rather than a zero-capacity pool when no report is usable", () => {
		expect(availableCapacity([])).toEqual({ capacity: undefined, included: 0 });
		const stale = cluster();
		stale.capacity!.stale = true;
		expect(availableCapacity([stale])).toEqual({
			capacity: undefined,
			included: 0,
		});
	});
});
