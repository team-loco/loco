import { describe, expect, test } from "bun:test";

import { DAY_MS } from "./time";

import {
	checkCustomRange,
	formatSpan,
	HOUR_MS,
	MINUTE_MS,
	parseSpan,
	presetsWithin,
	QUERY_MAX_MS,
	readWindow,
	shiftWindow,
	spanLabel,
	stepSeconds,
	writeWindow,
} from "./obs-time";

const NOW = Date.UTC(2026, 10, 8, 12, 0);

describe("time range text", () => {
	test("reads short and spelled-out spans", () => {
		expect(parseSpan("15m")).toBe(15 * MINUTE_MS);
		expect(parseSpan("90 min")).toBe(90 * MINUTE_MS);
		expect(parseSpan("Past 6 hours")).toBe(6 * HOUR_MS);
		expect(parseSpan("last 2d")).toBe(2 * DAY_MS);
		expect(parseSpan("1w")).toBe(7 * DAY_MS);
	});

	test("rejects text that is not a span", () => {
		expect(parseSpan("")).toBeNull();
		expect(parseSpan("0h")).toBeNull();
		expect(parseSpan("6mo")).toBeNull();
		expect(parseSpan("soon")).toBeNull();
	});

	test("writes spans back in the largest whole unit", () => {
		expect(formatSpan(90 * MINUTE_MS)).toBe("90m");
		expect(formatSpan(4 * HOUR_MS)).toBe("4h");
		expect(formatSpan(DAY_MS)).toBe("24h");
		expect(formatSpan(2 * DAY_MS)).toBe("2d");
		expect(spanLabel(HOUR_MS)).toBe("Past hour");
		expect(spanLabel(DAY_MS)).toBe("Past 24 hours");
		expect(spanLabel(15 * MINUTE_MS)).toBe("Past 15 minutes");
	});
});

describe("time range limits", () => {
	test("offers no preset longer than the limit", () => {
		expect(presetsWithin(QUERY_MAX_MS).every((ms) => ms <= QUERY_MAX_MS)).toBe(true);
		expect(presetsWithin(QUERY_MAX_MS)).toContain(QUERY_MAX_MS);
	});

	test("clamps a linked range to the limit", () => {
		expect(readWindow(new URLSearchParams("range=7d"), QUERY_MAX_MS)).toEqual({
			kind: "relative",
			spanMs: QUERY_MAX_MS,
		});
		const from = NOW - 3 * DAY_MS;
		expect(readWindow(new URLSearchParams(`from=${String(from)}&to=${String(NOW)}`), QUERY_MAX_MS)).toEqual({
			kind: "absolute",
			from: NOW - QUERY_MAX_MS,
			to: NOW,
		});
	});

	test("falls back to the default for unreadable bounds", () => {
		expect(readWindow(new URLSearchParams("from=abc&to=1"), QUERY_MAX_MS)).toEqual({
			kind: "relative",
			spanMs: HOUR_MS,
		});
	});

	test("explains custom ranges the query can't run", () => {
		expect(checkCustomRange(NOW - HOUR_MS, NOW, NOW, QUERY_MAX_MS)).toBeNull();
		expect(checkCustomRange(NOW, NOW - HOUR_MS, NOW, QUERY_MAX_MS)).toBe("order");
		expect(checkCustomRange(NOW + HOUR_MS, NOW + 2 * HOUR_MS, NOW, QUERY_MAX_MS)).toBe("future");
		expect(checkCustomRange(NOW - 2 * DAY_MS, NOW, NOW, QUERY_MAX_MS)).toBe("too-long");
		expect(checkCustomRange(Number.NaN, NOW, NOW, QUERY_MAX_MS)).toBe("invalid");
	});
});

describe("moving the time range", () => {
	test("earlier keeps the span and later returns to live at the present", () => {
		const earlier = shiftWindow({ kind: "relative", spanMs: HOUR_MS }, -1, NOW);
		expect(earlier).toEqual({ kind: "absolute", from: NOW - 2 * HOUR_MS, to: NOW - HOUR_MS });
		expect(shiftWindow(earlier, 1, NOW)).toEqual({ kind: "relative", spanMs: HOUR_MS });
	});

	test("round-trips through the URL", () => {
		const absolute = { kind: "absolute" as const, from: NOW - HOUR_MS, to: NOW };
		expect(readWindow(writeWindow(new URLSearchParams("range=1h"), absolute), QUERY_MAX_MS)).toEqual(absolute);
		const relative = { kind: "relative" as const, spanMs: 4 * HOUR_MS };
		expect(readWindow(writeWindow(new URLSearchParams("from=1&to=2"), relative), QUERY_MAX_MS)).toEqual(relative);
	});

	test("metric steps keep about sixty points and never go below a minute", () => {
		expect(stepSeconds(15 * MINUTE_MS)).toBe(60);
		expect(stepSeconds(DAY_MS)).toBe(1440);
	});
});
