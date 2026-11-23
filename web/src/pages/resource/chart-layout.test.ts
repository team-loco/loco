import { describe, expect, test } from "bun:test";

import { chartMax, fitLabel, placeMarkers } from "./chart-layout";

describe("chartMax", () => {
	test("leaves room above the limit", () => {
		expect(chartMax(0.4, 1)).toBe(1.2);
		expect(chartMax(0.1, 2048)).toBe(2500);
	});

	test("rises to the peak when it passes the limit", () => {
		expect(chartMax(3, 1)).toBe(4);
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
