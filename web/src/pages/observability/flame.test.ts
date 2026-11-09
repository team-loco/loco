import { describe, expect, test } from "bun:test";
import { create } from "@bufbuild/protobuf";
import { TraceSpanSchema } from "@gen/loco/observability/v1/observability_pb";

import { failingSpan, flameLayout, FULL_ZOOM, lineage, selfTime, serviceShares, zoomTo } from "./flame";
import { summarizeTraces } from "./traces";

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
	test("places each span by start offset and duration on the row of its depth", () => {
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
		expect(shares.find((s) => s.service === "api")?.nanos).toBe(600n);
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
});
