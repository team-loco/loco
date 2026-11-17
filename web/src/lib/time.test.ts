import { expect, test } from "bun:test";

import { shortAgoLabel, shortDurationLabel } from "./time";

test("shortAgoLabel fits minutes, hours and days in a few characters", () => {
	const now = Date.UTC(2026, 0, 10);
	expect(shortAgoLabel(now - 10_000, now)).toBe("now");
	expect(shortAgoLabel(now - 5 * 60_000, now)).toBe("5m ago");
	expect(shortAgoLabel(now - 3 * 3_600_000, now)).toBe("3h ago");
	expect(shortAgoLabel(now - 2 * 86_400_000, now)).toBe("2d ago");
});

test("formats short durations with rounded seconds, minutes, hours and days", () => {
	for (const [ms, label] of [
		[0, "1s"],
		[10_000, "10s"],
		[29_000, "29s"],
		[30_000, "1m"],
		[5 * 60_000, "5m"],
		[59.5 * 60_000, "1h"],
		[3 * 3_600_000, "3h"],
		[23.5 * 3_600_000, "1d"],
		[2 * 86_400_000, "2d"],
	] as const)
		expect(shortDurationLabel(ms)).toBe(label);
});

import {
	DAY_MS,
	formatStarted,
	formatAxisTime,
	formatClock,
	formatFullTime,
	formatIsoDate,
	parseClockInput,
	timeAxis,
} from "./time";

const utc12 = { zone: "utc", clock: "12" } as const;
const midnight = Date.UTC(2026, 10, 12);

test("formats midnight, noon, milliseconds and the timestamp zone", () => {
	expect(formatClock(midnight, true, utc12)).toBe("12:00:00.000 AM");
	expect(formatClock(midnight + 12 * 3_600_000 + 123, true, utc12)).toBe("12:00:00.123 PM");
	expect(formatFullTime(midnight, false, utc12)).toBe("Nov 12, 2026, 12:00:00 AM UTC");
	expect(formatIsoDate(midnight, utc12)).toBe("2026-11-12");
});

test("places ticks on round boundaries within an unaligned range", () => {
	const from = midnight + 123_000;
	const ticks = timeAxis(from, from + 3_600_000, utc12);
	expect(ticks.length).toBeGreaterThanOrEqual(6);
	expect(ticks.length).toBeLessThanOrEqual(9);
	expect(ticks.every((t) => t >= from && t <= from + 3_600_000 && t % 300_000 === 0)).toBe(true);
	expect(timeAxis(from, from, utc12)).toEqual([]);
});

test("parses clock input without changing midnight and noon", () => {
	expect(parseClockInput("12:00 AM", utc12)).toBe("00:00");
	expect(parseClockInput("12:00 PM", utc12)).toBe("12:00");
	expect(parseClockInput("1:04 pm", utc12)).toBe("13:04");
	for (const value of ["13:00 PM", "0:00 AM", "1:60 PM", "1:00"]) expect(parseClockInput(value, utc12)).toBeNull();
	expect(parseClockInput("23:59", { zone: "utc", clock: "24" })).toBe("23:59");
});

test("daily local ticks stay at midnight through daylight saving changes", () => {
	const from = new Date(2026, 2, 5).getTime();
	const to = new Date(2026, 2, 12).getTime();
	const ticks = timeAxis(from, to, { zone: "local", clock: "24" });
	expect(ticks.length).toBe(8);
	expect(ticks.every((t) => new Date(t).getHours() === 0 && new Date(t).getMinutes() === 0)).toBe(true);
	expect(new Set(ticks.map((t) => formatIsoDate(t, { zone: "local", clock: "24" }))).size).toBe(8);
});

test("local date and clock stay together near a daylight saving boundary", () => {
	const instant = new Date(2026, 2, 8, 3, 4, 5, 123).getTime();
	const pref = { zone: "local", clock: "24" } as const;
	expect(formatClock(instant, true, pref)).toBe("03:04:05.123");
	expect(formatIsoDate(instant, pref)).toBe("2026-03-08");
});

test("daily ticks return to midnight after a midnight clock change", () => {
	const pref = { zone: "local", clock: "24" } as const;
	const ticks = timeAxis(new Date(2026, 8, 3).getTime(), new Date(2026, 8, 10).getTime(), pref);
	expect(ticks.length).toBe(8);
	for (const tick of ticks) {
		const d = new Date(tick);
		expect(tick).toBe(new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime());
	}
});

test("spreads ticks across the requested range, independent of sparse samples", () => {
	const from = new Date(2026, 9, 19, 12).getTime();
	const hour = DAY_MS / 24;
	const ticks = timeAxis(from, from + hour, { zone: "local", clock: "24" });
	expect(ticks).toEqual([from, ...Array.from({ length: 6 }, (_, i) => from + ((i + 1) * hour) / 6)]);
});

test("labels a day-long window with clock times", () => {
	const from = new Date(2026, 9, 19, 12).getTime();
	const pref = { zone: "local", clock: "24" } as const;
	expect(formatAxisTime(from, from, from + DAY_MS, pref)).toBe("12:00");
	expect(formatAxisTime(from + DAY_MS / 24, from, from + DAY_MS, pref)).toBe("13:00");
});

test("formats start labels with explicit clock and zone preferences", () => {
	const instant = Date.UTC(2026, 10, 12, 13, 4);
	expect(formatStarted(instant, { zone: "utc", clock: "24" })).toBe("Nov 12, 13:04");
	expect(formatStarted(instant, utc12)).toBe("Nov 12, 1:04 PM");
	const local = new Date(2026, 10, 12, 13, 4).getTime();
	expect(formatStarted(local, { zone: "local", clock: "12" })).toBe("Nov 12, 1:04 PM");
});

test("leaves missing start times blank", () => {
	expect(formatStarted(0, utc12)).toBe("—");
	expect(formatStarted(undefined, utc12)).toBe("—");
});
