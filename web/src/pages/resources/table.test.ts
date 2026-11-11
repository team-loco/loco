import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { useTable, type ColumnSort } from "@tanstack/react-table";
import { dataTableFeatures, sortOptions, type DataTableInstance } from "@/components/design/DataTable";
import { create } from "@bufbuild/protobuf";
import { timestampFromMs } from "@bufbuild/protobuf/wkt";
import { describe, expect, test } from "bun:test";
import { DeploymentSchema, type Deployment } from "@gen/loco/deployment/v1/deployment_pb";
import { EnvironmentSchema } from "@gen/loco/environment/v1/environment_pb";
import { ResourceSchema, ResourceStatus, type Resource } from "@gen/loco/resource/v1/resource_pb";

import type { Draft } from "@/lib/drafts";

import { resourceColumns } from "./columns";

import {
	ALL,
	buildRows,
	cellSummary,
	draftRows,
	filterRows,
	isDraftRow,
	linkEnvironment,
	NOT_DEPLOYED,
	nameOrder,
	readTableState,
	regionCounts,
	replicasLabel,
	statusCounts,
	writeTableState,
	type Filter,
	type ListRow,
} from "./table";

function withTable<R>(rows: ListRow[], sorting: ColumnSort, read: (table: DataTableInstance<ListRow>) => R): R {
	let result: R | undefined;
	function Rows() {
		const table = useTable({
			...sortOptions,
			features: dataTableFeatures,
			columns,
			data: nameOrder(rows),
			state: { sorting: [sorting], pagination: { pageIndex: 0, pageSize: Math.max(1, rows.length) } },
		});
		result = read(table);
		return null;
	}
	renderToStaticMarkup(createElement(Rows));
	if (result === undefined) throw new Error("table did not render");
	return result;
}

function sortRows(rows: ListRow[], sorting: ColumnSort): ListRow[] {
	return withTable(rows, sorting, (table) => table.getRowModel().rows.map((row) => row.original));
}

const prod = create(EnvironmentSchema, { id: "e-prod", name: "production" });
const staging = create(EnvironmentSchema, { id: "e-stg", name: "staging" });
const envs = [prod, staging];
const columns = resourceColumns(envs);

interface RegionSeed {
	region: string;
	ready: number;
	desired: number;
	deploymentId?: string;
	message?: string;
}

function resource(
	name: string,
	envStatuses: { env: string; status: ResourceStatus; regions: RegionSeed[] }[] = [],
	patch: { description?: string; domain?: string; cpu?: string; memory?: string; createdMs?: number } = {},
): Resource {
	return create(ResourceSchema, {
		id: name,
		name,
		description: patch.description ?? "",
		domains: patch.domain === undefined ? [] : [{ domain: patch.domain, isPrimary: true }],
		createdAt: timestampFromMs(patch.createdMs ?? 0),
		spec: {
			spec: {
				case: "service",
				value: {
					regions: {
						"us-east-1": {
							enabled: true,
							cpu: patch.cpu ?? "500m",
							memory: patch.memory ?? "1Gi",
							minReplicas: 1,
							maxReplicas: 3,
						},
					},
				},
			},
		},
		environmentStatuses: envStatuses.map((e) => ({
			environmentId: e.env,
			status: e.status,
			regions: e.regions.map((r) => ({
				region: r.region,
				status: e.status,
				readyReplicas: r.ready,
				desiredReplicas: r.desired,
				deploymentId: r.deploymentId ?? "",
				message: r.message,
			})),
		})),
	});
}

function deployment(id: string, env: string, image: string, createdMs: number): Deployment {
	return create(DeploymentSchema, {
		id,
		environmentId: env,
		createdAt: timestampFromMs(createdMs),
		spec: { spec: { case: "service", value: { build: { image } } } },
	});
}

const everything: Filter = { query: "", status: ALL, region: ALL };

function names(rows: ListRow[]): string[] {
	return rows.map((r) => (isDraftRow(r) ? r.draft.name : r.resource.name));
}

function draft(name: string, edits: Draft["edits"] = {}): Draft {
	return { id: `d-${name}`, name, image: `ghcr.io/acme/${name}:1.2`, createdMs: 5, edits };
}

describe("buildRows", () => {
	test("each environment cell sums replicas and takes the version of its active deployment", () => {
		const api = resource("api", [
			{
				env: "e-prod",
				status: ResourceStatus.DEGRADED,
				regions: [
					{ region: "us-east-1", ready: 1, desired: 2, deploymentId: "d2", message: "Back-off restarting" },
					{ region: "eu-west-1", ready: 1, desired: 1, deploymentId: "d2" },
				],
			},
		]);
		const deps = [
			deployment("d1", "e-prod", "ghcr.io/acme/api:v1", 1000),
			deployment("d2", "e-prod", "ghcr.io/acme/api:v2", 2000),
			deployment("d3", "e-stg", "ghcr.io/acme/api:v3", 3000),
		];
		const [row] = buildRows([api], [deps], envs, undefined);
		const cell = row?.cells.get("e-prod");
		if (cell === undefined) throw new Error("no production cell");
		expect(cell.tag).toBe("v2");
		expect(cell.deployedMs).toBe(2000);
		expect(cell.note).toBe("Back-off restarting");
		expect(cellSummary(cell)).toBe("2/3 · us-east-1 +1");
		expect(row?.cells.has("e-stg")).toBe(false);
		expect(row?.image).toBe("ghcr.io/acme/api");
		expect(row?.cpuMilli).toBe(500);
		expect(row?.memMi).toBe(1024);
		expect(row === undefined ? null : replicasLabel(row)).toBe("1–3");
	});

	test("size and replicas follow the active environment's running deployment", () => {
		const api = resource("api", [
			{
				env: "e-prod",
				status: ResourceStatus.HEALTHY,
				regions: [{ region: "us-east-1", ready: 2, desired: 2, deploymentId: "d1" }],
			},
		]);
		const scaled = create(DeploymentSchema, {
			id: "d1",
			environmentId: "e-prod",
			spec: { spec: { case: "service", value: { memory: "512Mi", minReplicas: 2, maxReplicas: 2 } } },
		});
		const [inProd] = buildRows([api], [[scaled]], envs, "e-prod");
		expect(inProd?.cpuMilli).toBe(500);
		expect(inProd?.memMi).toBe(512);
		expect(inProd === undefined ? null : replicasLabel(inProd)).toBe("2");
		const [inStaging] = buildRows([api], [[scaled]], envs, "e-stg");
		expect(inStaging?.memMi).toBe(1024);
		expect(inStaging === undefined ? null : replicasLabel(inStaging)).toBe("1–3");
	});

	test("an environment with no reported regions has no cell", () => {
		const [row] = buildRows(
			[resource("api", [{ env: "e-prod", status: ResourceStatus.HEALTHY, regions: [] }])],
			[[]],
			envs,
			undefined,
		);
		expect(row?.cells.size).toBe(0);
	});
});

describe("filters", () => {
	const rows = buildRows(
		[
			resource(
				"api",
				[
					{ env: "e-prod", status: ResourceStatus.HEALTHY, regions: [{ region: "us-east-1", ready: 1, desired: 1 }] },
					{ env: "e-stg", status: ResourceStatus.DEGRADED, regions: [{ region: "eu-west-1", ready: 0, desired: 1 }] },
				],
				{ domain: "api.example.dev" },
			),
			resource(
				"worker",
				[{ env: "e-prod", status: ResourceStatus.DEPLOYING, regions: [{ region: "us-east-1", ready: 0, desired: 1 }] }],
				{ description: "Queue consumer" },
			),
			resource("draft"),
		],
		[[], [], []],
		envs,
		undefined,
	);

	test("a status matches when any environment has it, and not deployed means no environment", () => {
		expect(names(filterRows(rows, { ...everything, status: "degraded" }))).toEqual(["api"]);
		expect(names(filterRows(rows, { ...everything, status: "deploying" }))).toEqual(["worker"]);
		expect(names(filterRows(rows, { ...everything, status: NOT_DEPLOYED }))).toEqual(["draft"]);
	});

	test("search covers the name, description, address and regions", () => {
		expect(names(filterRows(rows, { ...everything, query: "QUEUE" }))).toEqual(["worker"]);
		expect(names(filterRows(rows, { ...everything, query: "example.dev" }))).toEqual(["api"]);
		expect(names(filterRows(rows, { ...everything, query: "eu-west" }))).toEqual(["api"]);
	});

	test("status counts follow the search and region but not the status", () => {
		const counts = statusCounts(rows, { ...everything, region: "us-east-1", status: "degraded" });
		expect(counts.get(ALL)).toBe(2);
		expect(counts.get("degraded")).toBe(1);
		expect(counts.get(NOT_DEPLOYED)).toBe(0);
	});

	test("region counts list every region in use and ignore the region and status filters", () => {
		expect(regionCounts(rows, { ...everything, region: "eu-west-1", status: "degraded" })).toEqual([
			{ region: "eu-west-1", count: 1 },
			{ region: "us-east-1", count: 2 },
		]);
	});
});

describe("sortRows", () => {
	const rows = buildRows(
		[
			resource(
				"b-healthy",
				[{ env: "e-prod", status: ResourceStatus.HEALTHY, regions: [{ region: "r", ready: 1, desired: 1 }] }],
				{ cpu: "2", createdMs: 1 },
			),
			resource("a-missing", [], { cpu: "250m", createdMs: 3 }),
			resource(
				"c-down",
				[{ env: "e-prod", status: ResourceStatus.UNAVAILABLE, regions: [{ region: "r", ready: 0, desired: 1 }] }],
				{ cpu: "1", createdMs: 2 },
			),
		],
		[[], [], []],
		envs,
		undefined,
	);

	test("an environment column puts problems first and undeployed resources last", () => {
		expect(names(sortRows(rows, { id: "env-production", desc: false }))).toEqual(["c-down", "b-healthy", "a-missing"]);
	});

	test("size and created sort numerically in either direction", () => {
		expect(names(sortRows(rows, { id: "size", desc: true }))).toEqual(["b-healthy", "c-down", "a-missing"]);
		expect(names(sortRows(rows, { id: "created", desc: false }))).toEqual(["b-healthy", "c-down", "a-missing"]);
	});

	test("size and created sort descending on the first click and every other column ascending", () => {
		const first = withTable(rows, { id: "name", desc: false }, (table) =>
			table
				.getAllLeafColumns()
				.filter((column) => column.getCanSort())
				.map((column) => [column.id, column.getFirstSortDir()]),
		);
		expect(Object.fromEntries(first)).toEqual({
			name: "asc",
			"env-production": "asc",
			"env-staging": "asc",
			size: "desc",
			created: "desc",
		});
	});

	test("ties sort by name ascending in either direction, with only the chosen column sorted", () => {
		const tied = buildRows(
			[resource("c"), resource("a"), resource("b", [], { cpu: "2" })],
			[[], [], []],
			envs,
			undefined,
		);
		expect(names(sortRows(tied, { id: "size", desc: true }))).toEqual(["b", "a", "c"]);
		expect(names(sortRows(tied, { id: "size", desc: false }))).toEqual(["a", "c", "b"]);
		const sorted = withTable(tied, { id: "size", desc: true }, (table) =>
			table
				.getAllLeafColumns()
				.filter((column) => column.getIsSorted() !== false)
				.map((column) => column.id),
		);
		expect(sorted).toEqual(["size"]);
	});
});

describe("draft rows", () => {
	const sizing = { cpu: "500m", memory: "1Gi", minReplicas: 1 };
	const resources = buildRows(
		[
			resource("healthy", [
				{ env: "e-prod", status: ResourceStatus.HEALTHY, regions: [{ region: "r", ready: 1, desired: 1 }] },
			]),
			resource("rolling", [
				{ env: "e-prod", status: ResourceStatus.DEPLOYING, regions: [{ region: "r", ready: 0, desired: 1 }] },
			]),
			resource("missing"),
		],
		[[], [], []],
		envs,
		"e-prod",
	);
	const drafts = draftRows([draft("web", { cpu: "2", replicas: 3 }), draft("api")], "e-prod", sizing);
	const rows: ListRow[] = [...resources, ...drafts];

	test("take sizing from the draft's edits, then the service defaults", () => {
		const [web, api] = drafts;
		expect([web?.cpuMilli, web?.memMi, web?.replicas]).toEqual([2000, 1024, { min: 3, max: 3 }]);
		expect([api?.cpuMilli, api?.replicas]).toEqual([500, { min: 1, max: 1 }]);
		expect(draftRows([draft("x")], "e-prod", undefined)[0]?.replicas).toBeNull();
		expect(api?.image).toBe("ghcr.io/acme/api");
	});

	test("sort between deploying and healthy in their environment's column", () => {
		expect(names(sortRows(rows, { id: "env-production", desc: false }))).toEqual([
			"rolling",
			"api",
			"web",
			"healthy",
			"missing",
		]);
	});

	test("sort with the undeployed in another environment's column", () => {
		expect(names(sortRows(rows, { id: "env-staging", desc: false }))).toEqual([
			"api",
			"healthy",
			"missing",
			"rolling",
			"web",
		]);
	});

	test("match their name and image in a search and count as not deployed", () => {
		expect(names(filterRows(rows, { ...everything, query: "acme/web" }))).toEqual(["web"]);
		expect(names(filterRows(rows, { ...everything, query: "not deployed" }))).toEqual([]);
		expect(names(filterRows(rows, { ...everything, status: NOT_DEPLOYED }))).toEqual(["missing", "web", "api"]);
	});
});

describe("linkEnvironment", () => {
	const [row] = buildRows(
		[
			resource("api", [
				{ env: "e-stg", status: ResourceStatus.HEALTHY, regions: [{ region: "r", ready: 1, desired: 1 }] },
			]),
		],
		[[]],
		envs,
		undefined,
	);

	test("prefers the active environment and falls back to one the resource runs in", () => {
		if (row === undefined) throw new Error("no row");
		expect(linkEnvironment(row, envs, prod)?.name).toBe("staging");
		expect(linkEnvironment(row, envs, staging)?.name).toBe("staging");
	});
});

describe("URL state", () => {
	const shown = {
		"env-production": true,
		"env-staging": true,
		size: true,
		replicas: false,
		cost: true,
		address: false,
		image: false,
		created: false,
	};

	test("defaults leave the URL alone and keep unrelated params", () => {
		const params = new URLSearchParams("env=staging");
		const state = readTableState(params, columns);
		expect(state.sorting).toEqual({ id: "env-production", desc: false });
		expect(state.visibility).toEqual(shown);
		expect(writeTableState(params, state, columns).toString()).toBe("env=staging");
	});

	test("round-trips filters, sort, page, size and columns", () => {
		const params = new URLSearchParams();
		const state = {
			filter: { query: "api", status: "degraded", region: "us-east-1" },
			sorting: { id: "created", desc: true },
			page: 2,
			size: 50,
			visibility: { ...shown, "env-production": false, size: false, cost: false, image: true },
		};
		const written = writeTableState(params, state, columns);
		expect(written.get("cols")).toBe("env-staging,image");
		expect(readTableState(written, columns)).toEqual(state);
	});

	test("ignores values the table cannot show", () => {
		const state = readTableState(
			new URLSearchParams("status=bogus&sort=image.asc&size=7&page=0&cols=nope,size"),
			columns,
		);
		expect(state.filter.status).toBe(ALL);
		expect(state.sorting.id).toBe("env-production");
		expect(state.size).toBe(25);
		expect(state.page).toBe(0);
		expect(Object.entries(state.visibility).filter(([, on]) => on)).toEqual([["size", true]]);
	});

	test("an empty column list hides every optional column", () => {
		const { visibility } = readTableState(new URLSearchParams("cols="), columns);
		expect(Object.values(visibility).some((on) => on)).toBe(false);
	});
});
