import { describe, expect, test } from "bun:test";
import { createLoader, createSerializer } from "nuqs";

import { DAY_MS } from "./time";

import {
	toLocalInput,
	fromLocalInput,
	checkCustomRange,
	formatSpan,
	HOUR_MS,
	joinLocalInput,
	MINUTE_MS,
	msParser,
	parseSpan,
	presetsWithin,
	QUERY_MAX_MS,
	shiftWindow,
	spanLabel,
	stepSeconds,
	toWindow,
	WINDOW_PARSERS,
	windowValues,
	windowLabel,
} from "./obs-time";

const loadWindow = createLoader(WINDOW_PARSERS);
const serializeWindow = createSerializer(WINDOW_PARSERS);
const readWindow = (search: string) => toWindow(loadWindow(search), QUERY_MAX_MS);

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
		expect(readWindow("?range=7d")).toEqual({
			kind: "relative",
			spanMs: QUERY_MAX_MS,
		});
		const from = NOW - 3 * DAY_MS;
		expect(readWindow(`?from=${String(from)}&to=${String(NOW)}`)).toEqual({
			kind: "absolute",
			from: NOW - QUERY_MAX_MS,
			to: NOW,
		});
	});

	test("reads a timestamp only as a positive safe integer", () => {
		expect(msParser.parse("1792130909123")).toBe(1792130909123);
		for (const value of ["", "NaN", "Infinity", "-1", "0", "1.5", "9007199254740992"]) {
			expect(msParser.parse(value)).toBeNull();
		}
	});

	test("falls back to the default for unreadable bounds", () => {
		expect(readWindow("?from=abc&to=1")).toEqual({
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
		expect(readWindow(serializeWindow("?range=1h", windowValues(absolute)))).toEqual(absolute);
		const relative = { kind: "relative" as const, spanMs: 4 * HOUR_MS };
		expect(readWindow(serializeWindow("?from=1&to=2", windowValues(relative)))).toEqual(relative);
	});

	test("metric steps keep about sixty points and never go below a minute", () => {
		expect(stepSeconds(15 * MINUTE_MS)).toBe(60);
		expect(stepSeconds(DAY_MS)).toBe(1440);
	});

	test("a time without a date stays empty", () => {
		expect(joinLocalInput("", "12:00")).toBe("");
		expect(joinLocalInput("2026-11-08", "12:00")).toBe("2026-11-08T12:00");
	});
});

test("absolute UTC inputs round-trip the selected instant", () => {
	const pref = { zone: "utc", clock: "12" } as const;
	const instant = Date.UTC(2026, 10, 12, 13, 4);
	expect(toLocalInput(instant, pref)).toBe("2026-11-12T13:04");
	expect(fromLocalInput(toLocalInput(instant, pref), pref)).toBe(instant);
});

test("custom inputs reject dates and times the calendar would normalize", () => {
	const pref = { zone: "utc", clock: "24" } as const;
	for (const value of ["2026-02-30T12:00", "2026-11-12T24:00", "2026-11-12T12:"]) {
		expect(fromLocalInput(value, pref)).toBeNaN();
	}
});

test("a nonexistent local clock time is rejected rather than shifted", () => {
	const value = "2026-03-08T02:30";
	const pref = { zone: "local", clock: "24" } as const;
	const instant = new Date(value).getTime();
	if (toLocalInput(instant, pref) !== value) expect(fromLocalInput(value, pref)).toBeNaN();
	else expect(fromLocalInput(value, pref)).toBe(instant);
});

test("absolute range labels use the explicit clock and zone preference", () => {
	const range = { kind: "absolute", from: Date.UTC(2026, 10, 12, 23, 50), to: Date.UTC(2026, 10, 13, 0, 10) } as const;
	expect(windowLabel(range, { zone: "utc", clock: "12" })).toBe("Nov 12 11:50 PM – Nov 13 12:10 AM");
	expect(windowLabel(range, { zone: "utc", clock: "24" })).toBe("Nov 12 23:50 – Nov 13 00:10");
});
