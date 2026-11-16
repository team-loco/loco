import { describe, expect, test } from "bun:test";

import { formatPercent, percentAxis, timeAxis } from "./compute-chart";

describe("compute chart axes", () => {
	test("covers the trace infrastructure window with evenly spaced time ticks", () => {
		const from = Date.UTC(2026, 0, 1, 12, 0);
		const to = Date.UTC(2026, 0, 1, 12, 20);
		expect(timeAxis(from, to)).toEqual([
			from,
			Date.UTC(2026, 0, 1, 12, 5),
			Date.UTC(2026, 0, 1, 12, 10),
			Date.UTC(2026, 0, 1, 12, 15),
			to,
		]);
	});
	test("keeps a comparable 0–100% scale for low usage and labels intermediate levels", () => {
		expect(percentAxis(3)).toEqual({ maximum: 100, ticks: [0, 25, 50, 75, 100] });
	});
	test("retains utilization over 100% without clipping or irregular axis levels", () => {
		expect(percentAxis(147)).toEqual({ maximum: 200, ticks: [0, 50, 100, 150, 200] });
	});
	test("keeps subpercent utilization and exact zero legible", () => {
		expect(formatPercent(0)).toBe("0%");
		expect(formatPercent(0.25)).toBe("0.3%");
		expect(formatPercent(23.8)).toBe("24%");
	});
});
