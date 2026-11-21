import { describe, expect, test } from "bun:test";

import { formatPercent, limitAxis, percentAxis, quarterAxis } from "./compute-chart";

describe("compute chart axes", () => {
	test("keeps a comparable 0–100% scale for low usage and labels intermediate levels", () => {
		expect(percentAxis(3)).toEqual({
			maximum: 100,
			ticks: [0, 25, 50, 75, 100],
		});
	});
	test("retains utilization over 100% without clipping or irregular axis levels", () => {
		expect(percentAxis(147)).toEqual({
			maximum: 200,
			ticks: [0, 50, 100, 150, 200],
		});
	});
	test("leaves room above the limit and labels the quarters between zero and the top", () => {
		expect(quarterAxis(3)).toEqual({ maximum: 120, ticks: [30, 60, 90] });
		expect(quarterAxis(147)).toEqual({ maximum: 200, ticks: [50, 100, 150] });
	});
	test("keeps subpercent utilization and exact zero legible", () => {
		expect(formatPercent(0)).toBe("0%");
		expect(formatPercent(0.25)).toBe("0.3%");
		expect(formatPercent(23.8)).toBe("24%");
		expect(formatPercent(0.04)).toBe("<0.1%");
	});
	test("stops the axis just above the limit and extends it for bursts", () => {
		expect(limitAxis(500, 100)).toEqual({ maximum: 550, ticks: [0, 250, 500] });
		expect(limitAxis(500, 1000).maximum).toBe(1100);
	});
});
