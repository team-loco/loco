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
	trafficAxis,
	trafficPoints,
	trafficSeries,
	trafficTotals,
	trafficValue,
	windowSeries,
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
	it("reads each field at its own newest bucket when series end at different times", () => {
		const series = (value: number, ...times: number[]) => [
			create(MetricSeriesSchema, {
				resourceId: "a",
				points: times.map((time) => ({ timestamp: msToTimestamp(time), value })),
			}),
		];
		const points = trafficPoints([series(10, 0, 60_000), series(1, 0, 60_000), series(500, 120_000), series(90, 0)]);
		expect(latestTraffic(points, "a")).toEqual({
			resourceId: "a",
			time: 120_000,
			requests: 10,
			errors: 1,
			bytes: 500,
			latency: 90,
		});
		expect(latestTraffic(points)?.requests).toBe(10);
	});
});

describe("traffic over a range", () => {
	const points = [
		{ resourceId: "a", time: 0, requests: 2, errors: 1, bytes: 1000 },
		{ resourceId: "b", time: 0, requests: 1 },
		{ resourceId: "a", time: 60_000, requests: 4, errors: 0, bytes: 3000 },
	];
	it("turns per-second rates into totals per resource", () => {
		const totals = trafficTotals(points, "requests", 60_000, { startMs: 0, endMs: 120_000 });
		expect(totals).toEqual(
			new Map([
				["a", 360],
				["b", 60],
			]),
		);
		expect(sumTotals(totals)).toBe(420);
		expect(sumTotals(trafficTotals(points, "bytes", 0, { startMs: 0, endMs: 120_000 }))).toBe(0);
		expect(sumTotals(trafficTotals([], "bytes", 60_000, { startMs: 0, endMs: 120_000 }))).toBeUndefined();
	});
	it("totals a partial last bucket over the time it covers", () => {
		const stepMs = 1440_000;
		const startMs = 10 * stepMs;
		const endMs = startMs + 911_000;
		const partial = [{ resourceId: "a", time: startMs, requests: 50 / 911 }];
		expect(sumTotals(trafficTotals(partial, "requests", stepMs, { startMs: startMs - 5 * stepMs, endMs }))).toBeCloseTo(
			50,
		);
	});
	it("totals a first bucket that starts before the window over the time inside it", () => {
		const stepMs = 1440_000;
		const startMs = 10 * stepMs + 500_000;
		const clipped = [{ resourceId: "a", time: 10 * stepMs, requests: 30 / 940 }];
		expect(sumTotals(trafficTotals(clipped, "requests", stepMs, { startMs, endMs: startMs + 3 * stepMs }))).toBeCloseTo(
			30,
		);
	});
	it("sums buckets across resources in time order", () => {
		expect(trafficSeries(points, "requests")).toEqual([3, 4]);
		expect(trafficSeries(points, "requests", "b")).toEqual([1]);
	});
	it("spans the whole window with zero for buckets without data", () => {
		expect(windowSeries(points, "requests", 60_000, { startMs: 0, endMs: 5 * 60_000 })).toEqual([3, 4, 0, 0, 0]);
	});
	it("labels the change from the prior range", () => {
		expect(changeLabel(110, 100)).toBe("↑ 10%");
		expect(changeLabel(95, 100)).toBe("↓ 5.0%");
		expect(changeLabel(100, 100)).toBe("0%");
		expect(changeLabel(100.01, 100)).toBe("0%");
		expect(changeLabel(99.99, 100)).toBe("0%");
		expect(changeLabel(5, 0)).toBeUndefined();
		expect(changeLabel(undefined, 3)).toBeUndefined();
	});
	it("formats counts and bytes compactly", () => {
		expect([formatCompact(1234), formatCompact(12), formatCompact(50.2)]).toEqual(["1.2K", "12", "50"]);
		expect([formatBytes(512), formatBytes(1_500_000), formatBytes(412_000_000_000)]).toEqual([
			"512 B",
			"1.5 MB",
			"412 GB",
		]);
	});
});

describe("traffic values", () => {
	it("shows small non-zero values as below the smallest shown", () => {
		expect(trafficValue(0, "/s")).toBe("0/s");
		expect(trafficValue(0.004, "/s")).toBe("<0.01/s");
		expect(trafficValue(0.004, " B/s")).toBe("<0.01 B/s");
		expect(trafficValue(0.03, "/s")).toBe("0.03/s");
		expect(trafficValue(undefined, "/s")).toBe("—");
	});

	it("reads a latency from one second up in seconds", () => {
		expect(trafficValue(950, " ms")).toBe("950 ms");
		expect(trafficValue(1200, " ms")).toBe("1.2 s");
		expect(trafficValue(0.004, " ms")).toBe("<0.01 ms");
	});
});

describe("traffic axis", () => {
	it("never repeats a label", () => {
		for (const max of [0, 1e-6, 0.0001, 0.003, 0.03, 0.0301, 0.07, 1, 7.5, 123, 4_500, 2_600_000]) {
			const { ticks, format } = trafficAxis(max, "/s");
			const labels = ticks.map(format);
			expect(new Set(labels).size).toBe(labels.length);
			expect(ticks[0]).toBe(0);
			expect(ticks.at(-1) ?? 0).toBeGreaterThanOrEqual(max);
		}
	});

	it("spaces ticks evenly with decimals where the step needs them", () => {
		const { ticks, format } = trafficAxis(0.03, "/s");
		expect(ticks.map(format)).toEqual(["0/s", "0.01/s", "0.02/s", "0.03/s"]);
	});

	it("labels latency over one second in seconds", () => {
		const { ticks, format } = trafficAxis(2400, " ms");
		expect(ticks.map(format)).toEqual(["0 s", "1 s", "2 s", "3 s"]);
		expect(trafficAxis(800, " ms").ticks.map(trafficAxis(800, " ms").format)).toEqual([
			"0 ms",
			"200 ms",
			"400 ms",
			"600 ms",
			"800 ms",
		]);
	});
});
