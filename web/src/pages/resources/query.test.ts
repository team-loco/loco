import { create } from "@bufbuild/protobuf";
import { describe, expect, test } from "bun:test";
import { EnvironmentSchema } from "@gen/loco/environment/v1/environment_pb";
import { ResourceFacetsSchema, ResourceOrder, ResourceStatus } from "@gen/loco/resource/v1/resource_pb";

import type { Draft } from "@/lib/drafts";

import { resourcesOrder, shownDrafts, statusCounts, statusInput } from "./query";
import { ALL, DRAFT, NOT_DEPLOYED } from "./table";

function draft(name: string): Draft {
	return { id: `d-${name}`, name, slug: name, image: `ghcr.io/acme/${name}:1`, createdMs: 1, edits: {} };
}

describe("statusInput", () => {
	test("a status filters by that status, Not deployed by no status, All and Draft by neither", () => {
		expect(statusInput("degraded")).toEqual({ status: ResourceStatus.DEGRADED, notDeployed: false });
		expect(statusInput(NOT_DEPLOYED)).toEqual({ status: ResourceStatus.UNSPECIFIED, notDeployed: true });
		expect(statusInput(ALL)).toEqual({ status: ResourceStatus.UNSPECIFIED, notDeployed: false });
		expect(statusInput(DRAFT)).toEqual({ status: ResourceStatus.UNSPECIFIED, notDeployed: false });
	});
});

describe("statusCounts", () => {
	test("reads each status and Not deployed from the facets, All from the total and Draft from the drafts", () => {
		const facets = create(ResourceFacetsSchema, {
			statuses: [
				{ status: ResourceStatus.HEALTHY, count: 3 },
				{ status: ResourceStatus.DEGRADED, count: 1 },
			],
			notDeployed: 2,
		});
		expect(Object.fromEntries(statusCounts(6, facets, 1))).toEqual({
			[ALL]: 6,
			[DRAFT]: 1,
			[NOT_DEPLOYED]: 2,
			healthy: 3,
			degraded: 1,
		});
	});

	test("has only the Draft count until the counts load", () => {
		expect(Object.fromEntries(statusCounts(undefined, undefined, 0))).toEqual({ [DRAFT]: 0 });
	});
});

describe("shownDrafts", () => {
	const drafts = [draft("web"), draft("queue")];
	const everything = { query: "", status: ALL, region: ALL, type: ALL };
	const names = (shown: Draft[]) => shown.map((d) => d.name);

	test("match their name or image in a search under All and Draft", () => {
		expect(names(shownDrafts(drafts, everything))).toEqual(["web", "queue"]);
		expect(names(shownDrafts(drafts, { ...everything, query: "ACME/QUEUE" }))).toEqual(["queue"]);
		expect(names(shownDrafts(drafts, { ...everything, status: DRAFT, query: "web" }))).toEqual(["web"]);
	});

	test("are services with no status or region", () => {
		expect(shownDrafts(drafts, { ...everything, status: "healthy" })).toEqual([]);
		expect(shownDrafts(drafts, { ...everything, status: NOT_DEPLOYED })).toEqual([]);
		expect(shownDrafts(drafts, { ...everything, region: "us-va-1" })).toEqual([]);
		expect(shownDrafts(drafts, { ...everything, type: "database" })).toEqual([]);
		expect(names(shownDrafts(drafts, { ...everything, type: "service" }))).toEqual(["web", "queue"]);
	});
});

describe("resourcesOrder", () => {
	const environments = [
		create(EnvironmentSchema, { id: "e-prod", slug: "production" }),
		create(EnvironmentSchema, { id: "e-stg", slug: "staging" }),
	];

	test("an environment's column sorts by its status in that environment", () => {
		expect(resourcesOrder({ id: "env-staging", desc: true }, environments)).toEqual({
			orderBy: ResourceOrder.STATUS,
			descending: true,
			environmentId: "e-stg",
		});
	});

	test("name, size and created sort across every environment", () => {
		for (const [id, orderBy] of [
			["name", ResourceOrder.NAME],
			["size", ResourceOrder.SIZE],
			["created", ResourceOrder.CREATED],
		] as const) {
			expect(resourcesOrder({ id, desc: false }, environments)).toEqual({
				orderBy,
				descending: false,
				environmentId: "",
			});
		}
	});
});
