import { expect, test } from "bun:test";
import { create } from "@bufbuild/protobuf";
import { ServiceGraphEdgeSchema } from "@gen/loco/observability/v1/observability_pb";

import { mapLayout } from "./mapLayout";
import { callLabel, serviceGraph } from "./serviceGraph";
import { row } from "./testRows";
import { itemName } from "./resourceRows";

const edge = create(ServiceGraphEdgeSchema, {
	from: "web",
	to: "api",
	requests: 3600n,
	errors: 90n,
	p95Ms: 12.34,
});

test("call metrics divide counts by the queried range and requests", () => {
	expect(callLabel(edge, 3_600_000)).toBe("1 req/s · 2.5% errors · p95 12.3 ms");
	expect(callLabel(edge, 1_800_000)).toBe("2 req/s · 2.5% errors · p95 12.3 ms");
	expect(callLabel(create(ServiceGraphEdgeSchema), 0)).toBe("0 req/s · 0% errors · p95 0 ms");
	expect(callLabel(create(ServiceGraphEdgeSchema, { requests: 1n }), 3_600_000)).toBe(
		"0.000278 req/s · 0% errors · p95 0 ms",
	);
});

test("calls join resource nodes and deduplicate external peers", () => {
	const layout = mapLayout([row("web"), row("api"), row("worker")], [], 1000, true);
	const graph = serviceGraph(
		layout,
		[
			edge,
			create(ServiceGraphEdgeSchema, {
				from: "api",
				to: "worker",
				requests: 2n,
			}),
			create(ServiceGraphEdgeSchema, {
				from: "api",
				to: "postgresql",
				requests: 2n,
			}),
			create(ServiceGraphEdgeSchema, {
				from: "worker",
				to: "postgresql",
				requests: 1n,
			}),
		],
		1000,
	);
	expect(graph.external.map((node) => node.name)).toEqual(["postgresql"]);
	expect(graph.external[0]?.x).toBeLessThan(1000);
	expect(graph.external[0]?.y).toBe(layout.internalNodes.find((node) => itemName(node.row) === "api")?.y);
	expect(graph.calls.map((call) => call.name)).toEqual([
		"web → api",
		"api → worker",
		"api → postgresql",
		"worker → postgresql",
	]);
	expect(graph.calls.every((call) => !call.path.includes("NaN"))).toBe(true);
	expect(graph.width).toBeGreaterThan(graph.external[0]?.x ?? 0);
	expect(serviceGraph(layout, [], 1000).calls).toEqual([]);
});

test("calls retain every deployed resource, including resources beyond the topology preview", () => {
	const rows = Array.from({ length: 12 }, (_, i) => row(`api-${i.toString()}`));
	const layout = mapLayout(rows, [], 1000, true);
	expect(layout.internalNodes).toHaveLength(12);
	expect(
		serviceGraph(layout, [create(ServiceGraphEdgeSchema, { from: "api-0", to: "api-11" })], 1000).external,
	).toEqual([]);
});

test("reverse calls and self calls have distinct directed paths", () => {
	const layout = mapLayout([row("web", { domain: "web.example.dev" }), row("api")], [], 1000, true);
	const graph = serviceGraph(
		layout,
		[
			edge,
			create(ServiceGraphEdgeSchema, { from: "api", to: "web" }),
			create(ServiceGraphEdgeSchema, { from: "api", to: "api" }),
		],
		1000,
	);
	expect(new Set(graph.calls.map((call) => call.path)).size).toBe(3);
	expect(new Set(graph.calls.map((call) => call.key)).size).toBe(3);
	expect(Math.abs((graph.calls[0]?.y ?? 0) - (graph.calls[1]?.y ?? 0))).toBeGreaterThanOrEqual(60);
});
