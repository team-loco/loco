import { expect, test } from "bun:test";
import { create } from "@bufbuild/protobuf";
import { TraceSpanSchema } from "@gen/loco/observability/v1/observability_pb";
import { traceJson } from "./trace-json";

test("exports exact nanoseconds, resource metadata, events and links as OTLP JSON", () => {
	const span = create(TraceSpanSchema, {
		traceId: "1234",
		spanId: "abcd",
		parentSpanId: "ef01",
		name: "GET /",
		kind: "Server",
		startTime: { seconds: 1_700_000_000n, nanos: 123 },
		durationNanos: 456n,
		statusCode: "Error",
		statusMessage: "timeout",
		resourceAttributes: { "service.name": "api" },
		scopeName: "http",
		scopeVersion: "1",
		spanAttributes: { method: "GET" },
		events: [
			{ name: "exception", timestamp: { seconds: 1_700_000_000n, nanos: 200 }, attributes: { message: "timeout" } },
		],
		links: [{ traceId: "5678", spanId: "abcd", attributes: { reason: "retry" } }],
	});
	expect(JSON.parse(traceJson([span])) as unknown).toMatchObject({
		resourceSpans: [
			{
				resource: { attributes: [{ key: "service.name", value: { stringValue: "api" } }] },
				scopeSpans: [
					{
						scope: { name: "http", version: "1" },
						spans: [
							{
								startTimeUnixNano: "1700000000000000123",
								endTimeUnixNano: "1700000000000000579",
								kind: 2,
								status: { code: 2, message: "timeout" },
								events: [{ timeUnixNano: "1700000000000000200" }],
								links: [{ traceId: "5678" }],
							},
						],
					},
				],
			},
		],
	});
});

for (const statusCode of ["Error", "STATUS_CODE_ERROR", "2"]) {
	test(`exports error status ${statusCode} and prefixed span kind`, () => {
		const span = create(TraceSpanSchema, { statusCode, kind: "SPAN_KIND_CLIENT" });
		expect(JSON.parse(traceJson([span]))).toMatchObject({
			resourceSpans: [{ scopeSpans: [{ spans: [{ kind: 3, status: { code: 2 } }] }] }],
		});
	});
}
