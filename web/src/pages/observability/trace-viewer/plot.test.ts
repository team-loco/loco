import { expect, test } from "bun:test";
import { boundedZoom, offsetTicks, zoomAround } from "./plot";

test("zoom anchors stay at the cursor and the window stays in the trace", () => {
	const zoom = { a: 0.2, b: 0.8 };
	const anchor = 0.25;
	const result = zoomAround(zoom, anchor, -100);
	expect(result.a + anchor * (result.b - result.a)).toBeCloseTo(zoom.a + anchor * (zoom.b - zoom.a));
	expect(result.b - result.a).toBeLessThan(zoom.b - zoom.a);
	expect(boundedZoom(-1, 0.2)).toEqual({ a: 0, b: 0.2 });
	expect(boundedZoom(1, 0.2)).toEqual({ a: 0.8, b: 1 });
	expect(zoomAround(zoom, 0.5, 10000)).toEqual({ a: 0, b: 1 });
});
test("offset ticks use round boundaries inside the zoomed window", () => {
	const ticks = offsetTicks(427_600_000n, { a: 0.25, b: 0.75 });
	expect(ticks.map((t) => t.nanos)).toEqual([150_000_000n, 200_000_000n, 250_000_000n, 300_000_000n]);
	expect(ticks.every((t) => t.left >= 0 && t.left <= 1)).toBe(true);
	expect(offsetTicks(0n, { a: 0, b: 1 })).toEqual([]);
});
