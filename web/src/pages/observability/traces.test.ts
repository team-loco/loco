import { describe, expect, test } from "bun:test";
import { create } from "@bufbuild/protobuf";
import { TraceSpanSchema } from "@gen/loco/observability/v1/observability_pb";
import { axisLabel, durationLabel, spanTree, summarizeTraces, uniqueSpans } from "./traces";

const span = (id: string, parent = "", nanos = 0) =>
	create(TraceSpanSchema, {
		traceId: "trace",
		spanId: id,
		parentSpanId: parent,
		startTime: { seconds: 1_800_000_000n, nanos },
		durationNanos: 100n,
	});

describe("trace timing and hierarchy", () => {
	test("axis uses one unit including zero and short ticks", () => {
		expect(axisLabel(0n, 2_000_000n)).toBe("0 ms");
		expect(axisLabel(500_000n, 2_000_000n)).toBe("0.50 ms");
	});
	test("retains nanosecond offsets at epoch timestamps beyond safe integers", () => {
		const root = span("root");
		const child = span("child", "root", 25);
		const trace = summarizeTraces([child, root])[0];
		expect(trace?.duration).toBe(125n);
		expect(durationLabel(25n)).toBe("25 ns");
		expect(durationLabel(1_500_000n)).toBe("1.50 ms");
	});
	test("builds a stable parent-first tree even when child arrives first and parent starts later", () => {
		const rows = spanTree([span("child", "root"), span("root", "", 20), span("missing", "unavailable", 30)]);
		expect(rows.map((row) => [row.span.spanId, row.depth, row.missingParent])).toEqual([
			["root", 0, false],
			["child", 1, false],
			["missing", 0, true],
		]);
	});
	test("cycles and self references terminate and remain inspectable", () => {
		const rows = spanTree([span("a", "b"), span("b", "a"), span("self", "self")]);
		expect(rows).toHaveLength(3);
		expect(new Set(rows.map((row) => row.span.spanId)).size).toBe(3);
	});
	test("deduplicates across pages without conflating different traces", () => {
		const first = span("shared");
		const other = create(TraceSpanSchema, { ...span("shared"), traceId: "other" });
		expect(uniqueSpans([first, first, other])).toHaveLength(2);
		expect(summarizeTraces([first, first, other])).toHaveLength(2);
	});
	test("duration includes children that outlive root and reports errors across services", () => {
		const root = span("root");
		const child = create(TraceSpanSchema, {
			...span("child", "root", 50),
			durationNanos: 200n,
			serviceName: "cart",
			statusCode: "Error",
		});
		const trace = summarizeTraces([root, child])[0];
		expect(trace?.duration).toBe(250n);
		expect(trace?.errors).toBe(1);
		expect(trace?.services).toBe(2);
	});
});
