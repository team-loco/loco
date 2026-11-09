import { create } from "@bufbuild/protobuf";
import { describe, expect, test } from "bun:test";
import { ResourceSchema, ResourceStatus } from "@gen/loco/resource/v1/resource_pb";

import { ALL, filterRows, MAP_PUBLIC_LIMIT, mapLayout, type RowFilter } from "./resourceRows";
import type { EnvResource, RegionReplicas } from "./useDashboardData";

function row(
	name: string,
	patch: { description?: string; domain?: string; status?: ResourceStatus; regions?: RegionReplicas[] } = {},
): EnvResource {
	const regions = patch.regions ?? [{ region: "us-east-1", desired: 1, ready: 1 }];
	const domains = patch.domain === undefined ? [] : [{ domain: patch.domain, isPrimary: true }];
	return {
		resource: create(ResourceSchema, { id: name, name, description: patch.description ?? "", domains }),
		environment: undefined,
		status: patch.status ?? ResourceStatus.HEALTHY,
		envDeployments: [],
		last: undefined,
		regions,
		totalReplicas: regions.reduce((n, g) => n + g.desired, 0),
		neverDeployed: false,
	};
}

const everything: RowFilter = { status: ALL, type: ALL, region: ALL, query: "" };

function names(rows: EnvResource[]): string[] {
	return rows.map((r) => r.resource.name);
}

describe("filterRows", () => {
	const rows = [
		row("api", { description: "Public storefront API", domain: "api.example.dev" }),
		row("worker", { status: ResourceStatus.UNAVAILABLE, regions: [{ region: "eu-west-1", desired: 2, ready: 0 }] }),
		row("billing", { status: ResourceStatus.DEPLOYING }),
	];

	test("search matches the description, the domain and the region", () => {
		expect(names(filterRows(rows, { ...everything, query: "storefront" }))).toEqual(["api"]);
		expect(names(filterRows(rows, { ...everything, query: "example.dev" }))).toEqual(["api"]);
		expect(names(filterRows(rows, { ...everything, query: "EU-WEST" }))).toEqual(["worker"]);
	});

	test("a status filter keeps only that status and All keeps every row", () => {
		expect(names(filterRows(rows, { ...everything, status: "unavailable" }))).toEqual(["worker"]);
		expect(names(filterRows(rows, { ...everything, status: "healthy" }))).toEqual(["api"]);
		expect(names(filterRows(rows, everything))).toEqual(["api", "worker", "billing"]);
	});
});

describe("mapLayout", () => {
	test("internal resources are grouped without edges", () => {
		const layout = mapLayout([row("web", { domain: "web.example.dev" }), row("queue")], ["us-east-1"]);
		expect(names(layout.internal)).toEqual(["queue"]);
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
		expect(layout.publicNodes[0]?.row.resource.name).toBe("z-broken");
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
