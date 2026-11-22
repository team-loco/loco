import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { useTable, type ColumnSort } from "@tanstack/react-table";
import { dataTableFeatures, sortOptions, type DataTableInstance } from "@/components/design/DataTable";
import { create } from "@bufbuild/protobuf";
import { timestampFromMs } from "@bufbuild/protobuf/wkt";
import { describe, expect, test } from "bun:test";
import { createLoader, createSerializer } from "nuqs";
import { DeploymentSchema, type Deployment } from "@gen/loco/deployment/v1/deployment_pb";
import { EnvironmentSchema } from "@gen/loco/environment/v1/environment_pb";
import { ResourceSchema, ResourceStatus, type Resource } from "@gen/loco/resource/v1/resource_pb";

import type { Draft } from "@/lib/drafts";

import { resourceColumns } from "./columns";

import {
	ALL,
	buildRows,
	FILTER_URL_KEYS,
	cellSummary,
	draftRows,
	linkEnvironment,
	replicasLabel,
	tableParsers,
	type ListRow,
} from "./table";

function withTable<R>(rows: ListRow[], sorting: ColumnSort, read: (table: DataTableInstance<ListRow>) => R): R {
	let result: R | undefined;
	function Rows() {
		const table = useTable({
			...sortOptions,
			features: dataTableFeatures,
			columns,
			data: rows,
			state: { sorting: [sorting], pagination: { pageIndex: 0, pageSize: Math.max(1, rows.length) } },
		});
		result = read(table);
		return null;
	}
	renderToStaticMarkup(createElement(Rows));
	if (result === undefined) throw new Error("table did not render");
	return result;
}

const prod = create(EnvironmentSchema, { id: "e-prod", slug: "production" });
const staging = create(EnvironmentSchema, { id: "e-stg", slug: "staging" });
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
		slug: name,
		name: name,
		description: patch.description ?? "",
		domains: patch.domain === undefined ? [] : [{ domain: patch.domain, isPrimary: true }],
		createdAt: timestampFromMs(patch.createdMs ?? 0),
		spec: {
			spec: {
				case: "service",
				value: {
					regions: {
						"us-va-1": {
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
		startedAt: timestampFromMs(createdMs + 500),
		spec: { spec: { case: "service", value: { build: { image } } } },
	});
}

function draft(name: string, edits: Draft["edits"] = {}): Draft {
	return { id: `d-${name}`, name, slug: name, image: `ghcr.io/acme/${name}:1.2`, createdMs: 5, edits };
}

describe("buildRows", () => {
	test("each environment cell sums replicas and takes the version of its active deployment", () => {
		const api = resource("api", [
			{
				env: "e-prod",
				status: ResourceStatus.DEGRADED,
				regions: [
					{ region: "us-va-1", ready: 1, desired: 2, deploymentId: "d2", message: "Back-off restarting" },
					{ region: "us-ca-1", ready: 1, desired: 1, deploymentId: "d2" },
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
		expect(cell.deployedMs).toBe(2500);
		expect(cell.note).toBe("Back-off restarting");
		expect(cellSummary(cell)).toBe("2/3, us-va-1 +1");
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
				regions: [{ region: "us-va-1", ready: 2, desired: 2, deploymentId: "d1" }],
			},
		]);
		const scaled = create(DeploymentSchema, {
			id: "d1",
			environmentId: "e-prod",
			spec: {
				spec: {
					case: "service",
					value: { regions: { "us-va-1": { memory: "512Mi", minReplicas: 2, maxReplicas: 2 } } },
				},
			},
		});
		const [inProd] = buildRows([api], [[scaled]], envs, "e-prod");
		expect(inProd?.cpuMilli).toBe(500);
		expect(inProd?.memMi).toBe(512);
		expect(inProd === undefined ? null : replicasLabel(inProd)).toBe("2");
		const [inStaging] = buildRows([api], [[scaled]], envs, "e-stg");
		expect(inStaging?.memMi).toBe(1024);
		expect(inStaging === undefined ? null : replicasLabel(inStaging)).toBe("1–3");
	});

	test("an environment with no reported regions says no region", () => {
		const [row] = buildRows(
			[resource("api", [{ env: "e-prod", status: ResourceStatus.HEALTHY, regions: [] }])],
			[[]],
			envs,
			undefined,
		);
		const cell = row?.cells.get("e-prod");
		if (cell === undefined) throw new Error("no production cell");
		expect(cellSummary(cell)).toBe("no region");
	});
});

describe("sort", () => {
	const rows = buildRows([resource("a"), resource("b")], [[], []], envs, undefined);

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
});

describe("draft rows", () => {
	const sizing = { cpu: "500m", memory: "1Gi", minReplicas: 1, cpuTarget: 65 };
	const drafts = draftRows([draft("web", { cpu: "2", replicas: 3 }), draft("api")], "e-prod", sizing, [], "");

	test("take sizing from the draft's edits, then the service defaults", () => {
		const [web, api] = drafts;
		expect([web?.cpuMilli, web?.memMi, web?.replicas]).toEqual([2000, 1024, { min: 3, max: 3 }]);
		expect([api?.cpuMilli, api?.replicas]).toEqual([500, { min: 1, max: 1 }]);
		expect(draftRows([draft("x")], "e-prod", undefined, [], "")[0]?.replicas).toBeNull();
		expect(api?.image).toBe("ghcr.io/acme/api");
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
		expect(linkEnvironment(row, envs, prod)?.slug).toBe("staging");
		expect(linkEnvironment(row, envs, staging)?.slug).toBe("staging");
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
	const parsers = tableParsers(columns);
	const load = createLoader(parsers, { urlKeys: FILTER_URL_KEYS });
	const serialize = createSerializer(parsers, { urlKeys: FILTER_URL_KEYS });

	test("defaults leave the URL alone and keep unrelated params", () => {
		const state = load("?env=staging");
		expect(state.sort).toEqual({ id: "env-production", desc: false });
		expect(state.cols).toEqual(shown);
		expect(serialize("?env=staging", state)).toBe("?env=staging");
	});

	test("round-trips filters, sort, page, size and columns", () => {
		const state = {
			query: "api",
			status: "degraded",
			region: "us-va-1",
			sort: { id: "created", desc: true },
			page: ["t1", "t2"],
			size: 50 as const,
			cols: { ...shown, "env-production": false, size: false, cost: false, image: true },
		};
		const written = serialize(state);
		expect(new URLSearchParams(written).get("cols")).toBe("env-staging,image");
		expect(load(written)).toEqual(state);
	});

	test("ignores values the table cannot show", () => {
		const state = load("?status=bogus&sort=image.asc&size=7&cols=nope,size");
		expect(state.status).toBe(ALL);
		expect(state.sort.id).toBe("env-production");
		expect(state.size).toBe(25);
		expect(state.page).toEqual([]);
		expect(Object.entries(state.cols).filter(([, on]) => on)).toEqual([["size", true]]);
	});

	test("an empty column list hides every optional column", () => {
		const { cols } = load("?cols=");
		expect(Object.values(cols).some((on) => on)).toBe(false);
	});
});
