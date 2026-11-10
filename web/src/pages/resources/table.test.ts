import { create } from "@bufbuild/protobuf";
import { timestampFromMs } from "@bufbuild/protobuf/wkt";
import { describe, expect, test } from "bun:test";
import { DeploymentSchema, type Deployment } from "@gen/loco/deployment/v1/deployment_pb";
import { EnvironmentSchema } from "@gen/loco/environment/v1/environment_pb";
import { ResourceSchema, ResourceStatus, type Resource } from "@gen/loco/resource/v1/resource_pb";

import {
	ALL,
	buildRows,
	cellSummary,
	filterRows,
	linkEnvironment,
	NOT_DEPLOYED,
	readTableState,
	regionCounts,
	replicasLabel,
	sortRows,
	statusCounts,
	tableColumns,
	writeTableState,
	type Filter,
	type ResourceRow,
} from "./table";

const prod = create(EnvironmentSchema, { id: "e-prod", name: "production" });
const staging = create(EnvironmentSchema, { id: "e-stg", name: "staging" });
const envs = [prod, staging];
const columns = tableColumns(envs);

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

function names(rows: ResourceRow[]): string[] {
	return rows.map((r) => r.resource.name);
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
		expect(names(sortRows(rows, { key: "env-production", dir: "asc" }, columns))).toEqual([
			"c-down",
			"b-healthy",
			"a-missing",
		]);
	});

	test("size and created sort numerically in either direction", () => {
		expect(names(sortRows(rows, { key: "size", dir: "desc" }, columns))).toEqual(["b-healthy", "c-down", "a-missing"]);
		expect(names(sortRows(rows, { key: "created", dir: "asc" }, columns))).toEqual([
			"b-healthy",
			"c-down",
			"a-missing",
		]);
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
	test("defaults leave the URL alone and keep unrelated params", () => {
		const params = new URLSearchParams("env=staging");
		const state = readTableState(params, columns);
		expect(state.sort).toEqual({ key: "env-production", dir: "asc" });
		expect(state.visible).toEqual(["env-production", "env-staging", "size", "cost"]);
		expect(writeTableState(params, state, columns).toString()).toBe("env=staging");
	});

	test("round-trips filters, sort, page, size and columns", () => {
		const params = new URLSearchParams();
		const state = {
			filter: { query: "api", status: "degraded", region: "us-east-1" },
			sort: { key: "created", dir: "desc" as const },
			page: 2,
			size: 50,
			visible: ["env-staging", "image"],
		};
		const written = writeTableState(params, state, columns);
		expect(readTableState(written, columns)).toEqual(state);
	});

	test("ignores values the table cannot show", () => {
		const state = readTableState(
			new URLSearchParams("status=bogus&sort=image.asc&size=7&page=0&cols=nope,size"),
			columns,
		);
		expect(state.filter.status).toBe(ALL);
		expect(state.sort.key).toBe("env-production");
		expect(state.size).toBe(25);
		expect(state.page).toBe(0);
		expect(state.visible).toEqual(["size"]);
	});

	test("an empty column list hides every optional column", () => {
		expect(readTableState(new URLSearchParams("cols="), columns).visible).toEqual([]);
	});
});
