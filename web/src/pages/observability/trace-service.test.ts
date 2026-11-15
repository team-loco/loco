import { expect, test } from "bun:test";
import { create } from "@bufbuild/protobuf";
import { TraceSpanSchema } from "@gen/loco/observability/v1/observability_pb";
import { traceColors, traceResourceKey, traceServiceLabel } from "./trace-service";

test("resolves only Loco's default service identity to the known workspace resource name", () => {
	const span = create(TraceSpanSchema, {
		resourceId: "id",
		serviceName: "resource-id",
	});
	expect(traceServiceLabel(span, "cart")).toBe("cart");
	expect(traceServiceLabel(span)).toBe("resource-id");
});

test("preserves explicit telemetry names instead of replacing them with the resource name", () => {
	const span = create(TraceSpanSchema, {
		resourceId: "id",
		serviceName: "payments-worker",
	});
	expect(traceServiceLabel(span, "cart")).toBe("payments-worker");
});

test("trace colours stay distinct beyond the chart palette and follow ranked order", () => {
	const keys = Array.from({ length: 24 }, (_, index) => `resource-${String(index)}`);
	const colors = traceColors(keys);
	expect(new Set(colors.values()).size).toBe(keys.length);
	expect(colors.get(keys[0] ?? "")).toBe("var(--series-0)");
	expect([...traceColors(keys).values()]).toEqual([...colors.values()]);
	expect(traceResourceKey({ resourceId: "first", serviceName: "api" })).not.toBe(
		traceResourceKey({ resourceId: "second", serviceName: "api" }),
	);
});
