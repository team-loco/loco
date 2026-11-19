import { describe, expect, test } from "bun:test";

import {
	bucketSeconds,
	countReadout,
	durationTick,
	errorRateLabel,
	latencyReadout,
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

	test("count readout names the bucket and its counts", () => {
		const start = Date.UTC(2026, 10, 15, 13, 5);
		const utc = { zone: "utc", clock: "24" } as const;
		const bucket = { start, widthSeconds: 60, traces: 42, errors: 3, p50: 12_000_000n, p95: 80_000_000n };
		expect(countReadout(bucket, utc)).toBe("13:05–13:06 · 42 traces · 3 errors");
		expect(countReadout({ ...bucket, widthSeconds: 30, traces: 0, errors: 0 }, utc)).toBe(
			"13:05:00–13:05:30 · 0 traces",
		);
		expect(countReadout({ ...bucket, traces: 1, errors: 0 }, utc)).toBe("13:05–13:06 · 1 trace");
		expect(countReadout(bucket, { zone: "utc", clock: "12" })).toStartWith("1:05 PM–1:06 PM · ");
	});

	test("latency readout names the bucket and its percentiles", () => {
		const start = Date.UTC(2026, 10, 15, 13, 5);
		const utc = { zone: "utc", clock: "24" } as const;
		const bucket = { start, widthSeconds: 60, traces: 42, errors: 3, p50: 12_000_000n, p95: 80_000_000n };
		expect(latencyReadout(bucket, utc)).toBe("13:05–13:06 · p50 12ms · p95 80ms");
		expect(latencyReadout({ ...bucket, traces: 0 }, utc)).toBe("13:05–13:06 · no traces");
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
