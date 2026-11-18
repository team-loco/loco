import { create } from "@bufbuild/protobuf";
import { describe, expect, it } from "bun:test";
import { MetricSeriesSchema } from "@gen/loco/observability/v1/observability_pb";
import { msToTimestamp } from "./time";
import {
	changeLabel,
	errorShare,
	formatBytes,
	formatCompact,
	latestTraffic,
	sumTotals,
	trafficPoints,
	trafficSeries,
	trafficTotals,
} from "./traffic";

describe("gateway traffic", () => {
	it("joins metrics by resource and bucket without converting rates again", () => {
		const series = (resourceId: string, value: number) =>
			create(MetricSeriesSchema, {
				resourceId,
				points: [{ timestamp: msToTimestamp(60_000), value }],
			});
		const points = trafficPoints([
			[series("a", 10), series("b", 30)],
			[series("a", 2), series("b", 1)],
			[series("a", 1000)],
			[series("a", 190)],
		]);
		expect(latestTraffic(points, "a")).toEqual({
			resourceId: "a",
			time: 60_000,
			requests: 10,
			errors: 2,
			bytes: 1000,
			latency: 190,
		});
		expect(errorShare(latestTraffic(points))).toBe(7.5);
	});
	it("treats an unused response class as zero only after its query resolves", () => {
		const requests = [
			create(MetricSeriesSchema, {
				resourceId: "a",
				points: [{ timestamp: msToTimestamp(60_000), value: 10 }],
			}),
		];
		expect(errorShare(latestTraffic(trafficPoints([requests, undefined])))).toBeUndefined();
		expect(errorShare(latestTraffic(trafficPoints([requests, []])))).toBe(0);
	});
	it("keeps missing samples distinct from recorded zero and avoids summing stale buckets", () => {
		expect(latestTraffic([])).toBeUndefined();
		expect(errorShare(undefined)).toBeUndefined();
		expect(errorShare({ resourceId: "a", time: 0, requests: 0 })).toBe(0);
		expect(errorShare({ resourceId: "a", time: 0, requests: 10 })).toBeUndefined();
		expect(
			latestTraffic([
				{ resourceId: "a", time: 1, requests: 100 },
				{ resourceId: "b", time: 2, requests: 2 },
			])?.requests,
		).toBe(2);
	});
});

describe("traffic over a range", () => {
	const points = [
		{ resourceId: "a", time: 0, requests: 2, errors: 1, bytes: 1000 },
		{ resourceId: "b", time: 0, requests: 1 },
		{ resourceId: "a", time: 60_000, requests: 4, errors: 0, bytes: 3000 },
	];
	it("turns per-second rates into totals per resource", () => {
		const totals = trafficTotals(points, "requests", 60_000);
		expect(totals).toEqual(
			new Map([
				["a", 360],
				["b", 60],
			]),
		);
		expect(sumTotals(totals)).toBe(420);
		expect(sumTotals(trafficTotals(points, "bytes", 0))).toBe(0);
		expect(sumTotals(trafficTotals([], "bytes", 60_000))).toBeUndefined();
	});
	it("sums buckets across resources in time order", () => {
		expect(trafficSeries(points, "requests")).toEqual([3, 4]);
		expect(trafficSeries(points, "requests", "b")).toEqual([1]);
	});
	it("labels the change from the prior range", () => {
		expect(changeLabel(110, 100)).toBe("↑ 10%");
		expect(changeLabel(95, 100)).toBe("↓ 5.0%");
		expect(changeLabel(5, 0)).toBeUndefined();
		expect(changeLabel(undefined, 3)).toBeUndefined();
	});
	it("formats counts and bytes compactly", () => {
		expect([formatCompact(1234), formatCompact(12)]).toEqual(["1.2K", "12"]);
		expect([formatBytes(512), formatBytes(1_500_000), formatBytes(412_000_000_000)]).toEqual([
			"512 B",
			"1.5 MB",
			"412 GB",
		]);
	});
});
