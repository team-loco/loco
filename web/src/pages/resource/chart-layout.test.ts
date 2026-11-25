import { describe, expect, test } from "bun:test";

import { chartMax, fitLabel, limitLabelX, placeMarkers } from "./chart-layout";

describe("chartMax", () => {
	test("leaves room above the limit", () => {
		expect(chartMax(0.4, 1, 1)).toBe(1.25);
		expect(chartMax(1, 100, 100)).toBe(125);
		expect(chartMax(8, 256, 256)).toBe(320);
		expect(chartMax(0.1, 2048, 2048)).toBe(2560);
	});

	test("expands for older limits only when they need more space", () => {
		expect(chartMax(20, 100, 110)).toBe(125);
		expect(chartMax(20, 100, 200)).toBeGreaterThan(200);
	});

	test("rises to the peak when it passes the limit", () => {
		expect(chartMax(3, 1, 1)).toBeGreaterThan(3);
		expect(chartMax(300, 100, 100) / 100).toBe(chartMax(768, 256, 256) / 256);
		expect(chartMax(0, 0, 0)).toBeGreaterThan(0);
	});
});

describe("fitLabel", () => {
	test("keeps a label that fits and cuts one that does not", () => {
		expect(fitLabel("crash loop started", 200, 6)).toBe("crash loop started");
		expect(fitLabel("deployed 3.1.0-accounting", 60, 6)).toBe("deployed …");
		expect(fitLabel("deployed", 12, 6)).toBeUndefined();
	});
});

describe("placeMarkers", () => {
	test("stops a label at the next line and flips late ones left", () => {
		const placed = placeMarkers(
			[
				{ x: 20, label: "deployed 3.1.0-accounting" },
				{ x: 70, label: "deployed 3.2.0" },
				{ x: 380, label: "crash loop started" },
			],
			0,
			400,
			6,
		);
		expect(placed.map((p) => p.label)).toEqual(["deplo…", "deployed 3.2.0", "crash loop started"]);
		expect(placed[2]?.flip).toBe(true);
		expect(placed[1]!.textX + 14 * 6).toBeLessThanOrEqual(380 - 4);
	});
});

describe("limitLabelX", () => {
	test("moves past a marker line that would cross the label", () => {
		expect(limitLabelX(40, 100, [20, 300], 400)).toBe(40);
		expect(limitLabelX(40, 100, [90, 300], 400)).toBe(95);
	});
});
