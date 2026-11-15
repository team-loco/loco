import { expect, test } from "bun:test";
import { create } from "@bufbuild/protobuf";
import { TraceSpanSchema } from "@gen/loco/observability/v1/observability_pb";
import { spanAttributes } from "./attributes";

test("attribute tabs retain custom resource fields and separate infrastructure namespaces", () => {
	const span = create(TraceSpanSchema, {
		spanAttributes: { "http.route": "/checkout" },
		resourceAttributes: {
			"loco.region": "west",
			"k8s.pod.name": "api-123",
			"container.id": "abc",
			"cloud.region": "west",
			"service.name": "api",
			"telemetry.sdk.name": "otel",
			"custom.version": "v1",
		},
		scopeName: "http",
		scopeVersion: "1",
	});
	const tabs = spanAttributes(span);
	expect(tabs.Attributes).toEqual([["http.route", "/checkout"]]);
	expect(tabs.Loco).toEqual([["loco.region", "west"]]);
	expect(tabs.Kubernetes).toHaveLength(3);
	expect(tabs.Telemetry).toContainEqual(["custom.version", "v1"]);
	expect(tabs.Telemetry).toContainEqual(["scope.version", "1"]);
	expect(Object.values(tabs).flat()).toHaveLength(10);
});
