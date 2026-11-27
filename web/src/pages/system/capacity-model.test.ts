import { describe, expect, test } from "bun:test";
import { create } from "@bufbuild/protobuf";
import { timestampFromMs } from "@bufbuild/protobuf/wkt";
import { ClusterCapacitySummarySchema, ClusterRole, ClusterSchema } from "@gen/loco/cluster/v1/cluster_pb";
import { availableCapacity, headroom, reservations } from "./capacity-model";

let nextId = 0;
function fixture() {
	const cluster = create(ClusterSchema, {
		id: `cluster-${nextId++}`,
		isActive: true,
		role: ClusterRole.APPS,
		healthStatus: "healthy",
		lastHeartbeat: timestampFromMs(1000),
	});
	const report = create(ClusterCapacitySummarySchema, {
		clusterId: cluster.id,
		sample: { id: "source:1000", reportedAt: timestampFromMs(1000) },
		cpuMillicoresAllocatable: 8000n,
		cpuMillicoresRequested: 3500n,
		memoryBytesAllocatable: 16n * 1024n ** 3n,
		memoryBytesRequested: 6n * 1024n ** 3n,
		capacityReportedAt: timestampFromMs(1000),
	});
	return { cluster, report };
}
function pool(items: ReturnType<typeof fixture>[]) {
	return availableCapacity(
		items.map((item) => item.cluster),
		new Map(items.map((item) => [item.cluster.id, item.report])),
	);
}

describe("reported resource headroom", () => {
	test("uses actual reservation totals and keeps a missing native dimension unknown", () => {
		const { report } = fixture();
		expect(reservations(report).map(headroom)).toEqual(["4.5 vCPU unreserved", "10 GiB unreserved"]);
		report.cpuMillicoresAllocatable = undefined;
		expect(reservations(report).map(headroom)).toEqual(["Not reported", "10 GiB unreserved"]);
		report.sample = undefined;
		expect(reservations(report).map(headroom)).toEqual(["Not reported", "Not reported"]);
	});
	test("reports oversubscription without clamping it to zero", () => {
		const { report } = fixture();
		report.cpuMillicoresRequested = 9000n;
		expect(headroom(reservations(report)[0]!)).toBe("1 vCPU over capacity");
	});
	test("excludes unhealthy, paused, removed, silent, stale and incomplete reports from pooled headroom", () => {
		const valid = [fixture(), fixture()];
		const unhealthy = fixture();
		unhealthy.cluster.healthStatus = "unhealthy";
		const paused = fixture();
		paused.cluster.isActive = false;
		const removed = fixture();
		removed.cluster.removedAt = timestampFromMs(1000);
		const silent = fixture();
		silent.cluster.silentSince = timestampFromMs(1000);
		const stale = fixture();
		stale.report.sample!.stale = true;
		const missing = fixture();
		missing.report.sample = undefined;
		const nativeStale = fixture();
		nativeStale.report.capacityStale = true;
		const nativeMissing = fixture();
		nativeMissing.report.capacityReportedAt = undefined;
		const result = pool([...valid, unhealthy, paused, removed, silent, stale, missing, nativeStale, nativeMissing]);
		expect(result.included).toBe(2);
		expect(result.capacity?.cpuMillicoresAllocatable).toBe(16000n);
		expect(result.capacity?.cpuMillicoresRequested).toBe(7000n);
	});
	test("excludes build-only and unknown-role clusters from the resource pool", () => {
		const resource = fixture();
		const combined = fixture();
		combined.cluster.role = ClusterRole.ALL;
		const builds = fixture();
		builds.cluster.role = ClusterRole.BUILDS;
		const unknown = fixture();
		unknown.cluster.role = ClusterRole.UNSPECIFIED;
		expect(pool([resource, combined, builds, unknown]).included).toBe(2);
	});
	test("preserves a known CPU total when the current memory capacity is unknown", () => {
		const first = fixture();
		const second = fixture();
		second.report.memoryBytesAllocatable = undefined;
		const result = pool([first, second]);
		expect(result.included).toBe(2);
		expect(result.capacity?.cpuMillicoresAllocatable).toBe(16000n);
		expect(result.capacity?.memoryBytesAllocatable).toBeUndefined();
		expect(reservations(result.capacity!).map(headroom)).toEqual(["9 vCPU unreserved", "Not reported"]);
	});

	test("retains independent oldest reservation and native-capacity timestamps", () => {
		const newest = fixture();
		newest.report.sample!.reportedAt = timestampFromMs(9000);
		newest.report.capacityReportedAt = timestampFromMs(2000);
		const oldest = fixture();
		oldest.report.sample!.reportedAt = timestampFromMs(3000);
		oldest.report.capacityReportedAt = timestampFromMs(4000);
		const result = pool([newest, oldest]);
		expect(result.capacity?.sample?.reportedAt).toEqual(timestampFromMs(3000));
		expect(result.capacity?.capacityReportedAt).toEqual(timestampFromMs(2000));
	});
	test("leaves a pool unavailable when no report is usable", () => {
		expect(pool([])).toEqual({ capacity: undefined, included: 0 });
		const missing = fixture();
		missing.report.sample = undefined;
		expect(pool([missing])).toEqual({ capacity: undefined, included: 0 });
	});
});
