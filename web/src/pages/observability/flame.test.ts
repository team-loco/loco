import { describe, expect, test } from "bun:test";
import { create } from "@bufbuild/protobuf";
import { TraceSpanSchema } from "@gen/loco/observability/v1/observability_pb";

import { descendantIds, failingSpan, flameLayout, FULL_ZOOM, lineage, selfTime, serviceShares, zoomTo } from "./flame";
import { spanStart, summarizeTraces } from "./traces";

const span = (id: string, parent: string, startNanos: number, duration: bigint, extra = {}) =>
	create(TraceSpanSchema, {
		traceId: "trace",
		spanId: id,
		parentSpanId: parent,
		startTime: { seconds: 1_800_000_000n, nanos: startNanos },
		durationNanos: duration,
		serviceName: "web",
		...extra,
	});

const root = span("root", "", 0, 1000n);
const api = span("api", "root", 100, 600n, { serviceName: "api" });
const db = span("db", "api", 200, 300n, { serviceName: "api", statusCode: "Error" });
const cache = span("cache", "root", 800, 100n);

function trace() {
	const summary = summarizeTraces([root, api, db, cache])[0];
	if (summary === undefined) throw new Error("no trace");
	return summary;
}

describe("flame graph layout", () => {
	test("places each span by start offset and duration on the first available row beneath its parent", () => {
		const { blocks, depth } = flameLayout(trace(), FULL_ZOOM);
		expect(depth).toBe(3);
		const dbBlock = blocks.find((b) => b.span.spanId === "db");
		expect(dbBlock?.depth).toBe(2);
		expect(dbBlock?.left).toBeCloseTo(0.2);
		expect(dbBlock?.width).toBeCloseTo(0.3);
	});

	test("zooming keeps only overlapping spans and clips them to the window", () => {
		const { blocks } = flameLayout(trace(), { a: 0.55, b: 0.75 });
		expect(blocks.map((b) => b.span.spanId).toSorted()).toEqual(["api", "root"]);
		const apiBlock = blocks.find((b) => b.span.spanId === "api");
		expect(apiBlock?.left).toBe(0);
		expect(apiBlock?.width).toBeCloseTo(0.75);
	});

	test("zooms to a span with a little padding and back out for the root", () => {
		const zoom = zoomTo(trace(), db);
		expect(zoom.a).toBeLessThan(0.2);
		expect(zoom.b).toBeGreaterThan(0.5);
		expect(zoomTo(trace(), root)).toEqual(FULL_ZOOM);
	});
});

describe("flame graph details", () => {
	test("self time subtracts direct children and shares sum per service", () => {
		expect(selfTime(trace(), root)).toBe(300n);
		expect(selfTime(trace(), api)).toBe(300n);
		const shares = serviceShares(trace());
		expect(shares.find((s) => s.key === "api")?.nanos).toBe(600n);
		expect(shares.reduce((sum, s) => sum + s.share, 0)).toBeCloseTo(1);
	});

	test("jumps to the deepest failing span unless it is already selected", () => {
		const parentError = span("api", "root", 100, 600n, { serviceName: "api", statusCode: "Error" });
		const summary = summarizeTraces([root, parentError, db])[0];
		if (summary === undefined) throw new Error("no trace");
		expect(failingSpan(summary, root)?.spanId).toBe("db");
		expect(failingSpan(summary, db)).toBeUndefined();
		const rootError = span("root", "", 0, 1000n, { statusCode: "Error" });
		const deep = summarizeTraces([rootError, api, db])[0];
		if (deep === undefined) throw new Error("no trace");
		expect(failingSpan(deep, rootError)?.spanId).toBe("db");
	});

	test("lineage covers ancestors and descendants, not siblings", () => {
		expect([...lineage(trace(), "api")].toSorted()).toEqual(["api", "db", "root"]);
	});

	test("descendants include the span and its children, not ancestors or siblings", () => {
		expect([...descendantIds(trace(), "api")]).toEqual(["api", "db"]);
	});
});

function summaryOf(spans: ReturnType<typeof span>[]) {
	const summary = summarizeTraces(spans)[0];
	if (summary === undefined) throw new Error("Missing trace");
	return summary;
}

test("concurrent siblings occupy separate rows beneath their parent", () => {
	const summary = summaryOf([root, span("a", "root", 100, 400n), span("b", "root", 200, 400n)]);
	const { blocks, depth } = flameLayout(summary, FULL_ZOOM);
	expect(blocks.map((b) => [b.span.spanId, b.depth])).toEqual([
		["root", 0],
		["a", 1],
		["b", 2],
	]);
	expect(depth).toBe(3);
});

test("a child outliving its parent cannot collide with the next sibling's child", () => {
	const summary = summaryOf([
		root,
		span("a", "root", 100, 200n),
		span("long", "a", 200, 500n),
		span("b", "root", 400, 300n),
		span("nested", "b", 450, 100n),
	]);
	const { blocks } = flameLayout(summary, FULL_ZOOM);
	expect(blocks.map((b) => [b.span.spanId, b.depth])).toEqual([
		["root", 0],
		["a", 1],
		["long", 2],
		["b", 1],
		["nested", 3],
	]);
	const zoomed = flameLayout(summary, { a: 0.4, b: 0.6 });
	for (const block of zoomed.blocks) {
		const original = blocks.find((b) => b.span.spanId === block.span.spanId);
		if (original === undefined) throw new Error("Missing unzoomed block");
		expect(block.depth).toBe(original.depth);
	}
});

test("random trace rows contain no overlapping intervals and follow their parents", () => {
	let seed = 9127;
	const random = (max: number) => {
		seed = (Math.imul(seed, 1664525) + 1013904223) >>> 0;
		return seed % max;
	};
	for (let run = 0; run < 100; run++) {
		const spans = [root];
		for (let index = 1; index < 80; index++)
			spans.push(
				span(String(index), spans[random(spans.length)]?.spanId ?? "root", random(1000), BigInt(random(500) + 1)),
			);
		const { blocks, depth } = flameLayout(summaryOf(spans), FULL_ZOOM);
		const byId = new Map(blocks.map((b) => [b.span.spanId, b]));
		expect(blocks).toHaveLength(spans.length);
		expect(depth).toBe(Math.max(...blocks.map((b) => b.depth)) + 1);
		for (const block of blocks) {
			const parent = byId.get(block.span.parentSpanId);
			if (parent !== undefined) expect(block.depth).toBeGreaterThan(parent.depth);
			const start = spanStart(block.span);
			const end = start + block.span.durationNanos;
			for (const other of blocks)
				if (other !== block && other.depth === block.depth) {
					const otherStart = spanStart(other.span);
					expect(start >= otherStart + other.span.durationNanos || end <= otherStart).toBe(true);
				}
		}
	}
});

test("self time subtracts the union of children clipped to the parent", () => {
	const parent = span("parent", "", 100, 1000n);
	const summary = summaryOf([
		parent,
		span("a", "parent", 50, 350n),
		span("b", "parent", 300, 300n),
		span("c", "parent", 900, 400n),
		span("outside", "parent", 1400, 100n),
	]);
	expect(selfTime(summary, parent)).toBe(300n);
});

test("ranks distinct resources separately when service names match", () => {
	const spans = [
		span("one", "", 0, 100n, { resourceId: "resource-one", serviceName: "api" }),
		span("two", "", 0, 200n, { resourceId: "resource-two", serviceName: "api" }),
	];
	const summary = summarizeTraces(spans)[0];
	if (summary === undefined) throw new Error("Missing trace");
	expect(serviceShares(summary)).toHaveLength(2);
	expect(serviceShares(summary).find((s) => s.key === "resource-two")?.nanos).toBe(200n);
});
