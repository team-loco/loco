import { describe, expect, test } from "bun:test";
import { ResourceStatus, ResourceType } from "@gen/loco/resource/v1/resource_pb";

import { ALL, filterRows, MAP_PUBLIC_LIMIT, mapLayout, type ResourceRow, type RowFilter } from "./resourceRows";

function row(name: string, patch: Partial<ResourceRow> = {}): ResourceRow {
	return {
		key: name,
		name,
		description: "",
		status: ResourceStatus.HEALTHY,
		type: ResourceType.SERVICE,
		domain: null,
		regions: [{ region: "us-east-1", desired: 1, ready: 1 }],
		item: undefined,
		...patch,
	};
}

const everything: RowFilter = { status: ALL, type: ALL, region: ALL, query: "" };

function names(rows: ResourceRow[]): string[] {
	return rows.map((r) => r.name);
}

describe("filterRows", () => {
	const rows = [
		row("api", { description: "Public storefront API", domain: "api.example.dev" }),
		row("worker", { status: ResourceStatus.UNAVAILABLE, regions: [{ region: "eu-west-1", desired: 2, ready: 0 }] }),
		row("billing", { status: "draft" }),
	];

	test("search matches the description, the domain and the region", () => {
		expect(names(filterRows(rows, { ...everything, query: "storefront" }))).toEqual(["api"]);
		expect(names(filterRows(rows, { ...everything, query: "example.dev" }))).toEqual(["api"]);
		expect(names(filterRows(rows, { ...everything, query: "EU-WEST" }))).toEqual(["worker"]);
	});

	test("a status filter keeps only that status and leaves drafts under All", () => {
		expect(names(filterRows(rows, { ...everything, status: "unavailable" }))).toEqual(["worker"]);
		expect(names(filterRows(rows, { ...everything, status: "healthy" }))).toEqual(["api"]);
		expect(names(filterRows(rows, everything))).toEqual(["api", "worker", "billing"]);
	});
});

describe("mapLayout", () => {
	test("internal resources are grouped without edges", () => {
		const layout = mapLayout([row("web", { domain: "web.example.dev" }), row("queue")], ["us-east-1"]);
		expect(layout.internal.map((r) => r.name)).toEqual(["queue"]);
		expect(layout.edges.map((e) => e.to)).toEqual(["web"]);
	});

	test("public resources past the limit are counted, not drawn", () => {
		const many = Array.from({ length: MAP_PUBLIC_LIMIT + 3 }, (_, i) =>
			row(`svc-${i.toString().padStart(2, "0")}`, { domain: `svc-${i.toString()}.example.dev` }),
		);
		const layout = mapLayout(many, ["us-east-1"]);
		expect(layout.publicNodes).toHaveLength(MAP_PUBLIC_LIMIT);
		expect(layout.hiddenPublic).toBe(3);
	});

	test("unhealthy resources are drawn before healthy ones when the map is capped", () => {
		const healthy = Array.from({ length: MAP_PUBLIC_LIMIT }, (_, i) =>
			row(`a-${i.toString()}`, { domain: `a-${i.toString()}.example.dev` }),
		);
		const broken = row("z-broken", { domain: "z.example.dev", status: ResourceStatus.DEGRADED });
		const layout = mapLayout([...healthy, broken], ["us-east-1"]);
		expect(layout.publicNodes[0]?.row.name).toBe("z-broken");
	});

	test("gateways of regions serving the same resources do not overlap", () => {
		const regions = [
			{ region: "us-east-1", desired: 1, ready: 1 },
			{ region: "eu-west-1", desired: 1, ready: 1 },
		];
		const layout = mapLayout([row("web", { domain: "web.example.dev", regions })], ["us-east-1", "eu-west-1"]);
		const [first, second] = layout.gateways;
		expect(first).toBeDefined();
		expect(second).toBeDefined();
		expect((second?.y ?? 0) - (first?.y ?? 0)).toBeGreaterThanOrEqual(layout.nodeHeight);
		expect(layout.height).toBeGreaterThanOrEqual((second?.y ?? 0) + layout.nodeHeight);
	});
});
