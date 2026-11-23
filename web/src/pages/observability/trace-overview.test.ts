import { describe, expect, test } from "bun:test";

import {
	bucketSeconds,
	bucketReadout,
	durationTick,
	errorRateLabel,
	filledRuns,
	logPosition,
	logScale,
	niceTicks,
} from "./trace-overview";

const MINUTE = 60_000;
const HOUR = 60 * MINUTE;

describe("trace overview", () => {
	test("bucket width keeps a range to at most 60 bars", () => {
		expect(bucketSeconds(MINUTE)).toBe(2);
		expect(bucketSeconds(15 * MINUTE)).toBe(30);
		expect(bucketSeconds(HOUR)).toBe(120);
		expect(bucketSeconds(24 * HOUR)).toBe(1800);
		expect(bucketSeconds(30 * 24 * HOUR)).toBe(86400);
	});

	test("ticks are round steps up to the maximum", () => {
		expect(niceTicks(19, 3)).toEqual([5, 10, 15]);
		expect(niceTicks(42, 3)).toEqual([10, 20, 30, 40]);
		expect(niceTicks(7.5, 3)).toEqual([2.5, 5, 7.5]);
		expect(niceTicks(1, 3)).toEqual([0.25, 0.5, 0.75, 1]);
		expect(niceTicks(0, 3)).toEqual([]);
	});

	test("error rate shows one decimal below 10%", () => {
		expect(errorRateLabel(0, 0)).toBe("no traces");
		expect(errorRateLabel(40, 0)).toBe("0% errors");
		expect(errorRateLabel(1000, 3)).toBe("0.3% errors");
		expect(errorRateLabel(4, 1)).toBe("25% errors");
	});

	test("duration ticks drop trailing zeros", () => {
		expect(durationTick(250e6)).toBe("250ms");
		expect(durationTick(2.5e6)).toBe("2.5ms");
		expect(durationTick(1e9)).toBe("1s");
		expect(durationTick(100)).toBe("100ns");
	});

	test("bucket readout names the span, counts and percentiles", () => {
		const start = Date.UTC(2026, 10, 15, 13, 5);
		const utc = { zone: "utc", clock: "24" } as const;
		const bucket = { start, widthSeconds: 60, traces: 42, errors: 3, p50: 12_000_000n, p95: 80_000_000n };
		expect(bucketReadout(bucket, utc)).toEqual({
			title: "13:05–13:06",
			rows: [
				["Traces", "42"],
				["Errors", "3"],
				["p50", "12ms"],
				["p95", "80ms"],
			],
		});
		expect(bucketReadout({ ...bucket, widthSeconds: 30, traces: 0, errors: 0 }, utc)).toEqual({
			title: "13:05:00–13:05:30",
			rows: [
				["Traces", "0"],
				["Errors", "0"],
			],
		});
		expect(bucketReadout(bucket, { zone: "utc", clock: "12" }).title).toBe("1:05 PM–1:06 PM");
	});

	test("latency axis spans decades so a 500 s outlier keeps millisecond values visible", () => {
		const scale = logScale([12e6, 80e6, 500e9]);
		expect(scale.min).toBe(1e6);
		expect(scale.max).toBe(1e12);
		expect(logPosition(12e6, scale)).toBeGreaterThan(0.15);
		expect(logPosition(500e9, scale)).toBeLessThan(1);
		expect(scale.ticks.length).toBeLessThanOrEqual(4);
	});
});

test("latency runs break at empty buckets and keep a lone bucket as its own run", () => {
	expect(filledRuns([0, 3, 0, 0])).toEqual([[1]]);
	expect(filledRuns([2, 1, 0, 4, 0, 5, 6])).toEqual([[0, 1], [3], [5, 6]]);
	expect(filledRuns([0, 0])).toEqual([]);
});
