import { expect, test } from "bun:test";
import { create } from "@bufbuild/protobuf";
import { TraceSpanSchema } from "@gen/loco/observability/v1/observability_pb";
import { traceServiceLabel } from "./trace-service";

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
