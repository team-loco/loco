import { describe, expect, test } from "bun:test";
import { create } from "@bufbuild/protobuf";
import { TraceSpanEventSchema, TraceSpanSchema } from "@gen/loco/observability/v1/observability_pb";
import {
	axisLabel,
	durationLabel,
	durationScale,
	eventOffsetLabel,
	spanTree,
	summarizeTraces,
	uniqueSpans,
} from "./traces";

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
	test("labels a span event by its offset from the span start", () => {
		const event = create(TraceSpanEventSchema, { timestamp: { seconds: 1_800_000_000n, nanos: 12_400_000 } });
		expect(eventOffsetLabel(span("root"), event)).toBe("+12.40 ms");
		expect(eventOffsetLabel(span("root"), create(TraceSpanEventSchema))).toBeNull();
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
		expect(rows.map((row) => [row.span.spanId, row.depth])).toEqual([
			["root", 0],
			["child", 1],
			["missing", 0],
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

describe("trace duration scale", () => {
	test("labels whole decades inside the range", () => {
		expect(durationScale([20, 40]).ticks.map((t) => t.label)).toEqual(["10 ms", "100 ms"]);
		expect(durationScale([3, 2500]).ticks.map((t) => t.label)).toEqual(["10 ms", "100 ms", "1 s"]);
	});
	test("maps a position back to the duration drawn there", () => {
		const scale = durationScale([5, 900]);
		expect(scale.msAt(scale.yOf(250))).toBeCloseTo(250);
		expect(scale.yOf(5)).toBe(1);
	});
});
