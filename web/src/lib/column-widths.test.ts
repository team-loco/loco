import { describe, expect, test } from "bun:test";
import { fitColumnWidths, resizeColumnPair, validColumnWidths } from "./column-widths";

describe("column widths", () => {
	test("keeps proportions when there is enough room", () => {
		expect(fitColumnWidths([43, 17, 20, 10, 10], [200, 108, 120, 52, 76], 1000)).toEqual([430, 170, 200, 100, 100]);
	});
	test("redistributes space without shrinking other columns below their minimum", () => {
		expect(fitColumnWidths([90, 5, 5], [180, 160, 64], 1000)).toEqual([776, 160, 64]);
	});
	test("keeps internal overflow usable when the viewport is too small", () => {
		expect(fitColumnWidths([42, 48, 10], [180, 160, 64], 200)).toEqual([180, 160, 64]);
	});
	test("constrains either side of a resize and leaves unrelated columns alone", () => {
		expect(resizeColumnPair([400, 300, 70], [180, 160, 64], 0, 800)).toEqual([540, 160, 70]);
		expect(resizeColumnPair([400, 300, 70], [180, 160, 64], 0, 0)).toEqual([180, 520, 70]);
		expect(resizeColumnPair([400, 300, 70], [180, 160, 64], 1, 200)).toEqual([400, 200, 170]);
	});
	test("rejects corrupt and incompatible saved preferences", () => {
		for (const value of [
			null,
			{},
			[1, 2],
			[1, 2, 0],
			[1, -2, 3],
			[1, "2", 3],
			[1, NaN, 3],
			[Infinity, 2, 3],
			[1e308, 1e308, 1e308],
		]) {
			expect(validColumnWidths(value, 3)).toBe(false);
		}
		expect(validColumnWidths([0.1, 0.2, 0.7], 3)).toBe(true);
	});
	test("fits extreme valid weights without non-finite geometry", () => {
		const widths = fitColumnWidths([1e308, 1, 1], [128, 112, 56], 1000);
		expect(widths.every(Number.isFinite)).toBe(true);
		expect(widths.reduce((sum, width) => sum + width, 0)).toBe(1000);
	});
});
