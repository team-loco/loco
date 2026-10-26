import { describe, expect, test } from "bun:test";

import { formatMetricTime, formatPercent, percentAxis, timeAxis } from "./compute-chart";
import { DAY_MS } from "./time";

const FROM = new Date(2026, 9, 19, 12).getTime();
const HOUR_MS = DAY_MS / 24;

describe("compute chart axes", () => {
	test("keeps a comparable 0–100% scale for low usage and labels intermediate levels", () => {
		expect(percentAxis(3)).toEqual({ maximum: 100, ticks: [0, 25, 50, 75, 100] });
	});
	test("retains utilization over 100% without clipping or irregular axis levels", () => {
		expect(percentAxis(147)).toEqual({ maximum: 200, ticks: [0, 50, 100, 150, 200] });
	});
	test("spreads ticks across the requested range, independent of sparse samples", () => {
		const ticks = timeAxis(FROM, FROM + HOUR_MS);
		expect(ticks).toEqual([FROM, FROM + HOUR_MS / 4, FROM + HOUR_MS / 2, FROM + (HOUR_MS * 3) / 4, FROM + HOUR_MS]);
	});
	test("keeps subpercent utilization and exact zero legible", () => {
		expect(formatPercent(0)).toBe("0%");
		expect(formatPercent(0.25)).toBe("0.3%");
		expect(formatPercent(23.8)).toBe("24%");
	});
	test("distinguishes times within the same date for a day-long window", () => {
		expect(formatMetricTime(FROM, FROM, FROM + DAY_MS)).toBe("10/19 12:00");
		expect(formatMetricTime(FROM + HOUR_MS, FROM, FROM + DAY_MS)).toBe("10/19 13:00");
	});
});
