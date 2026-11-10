import { describe, expect, test } from "bun:test";
import { ResourceStatus } from "@gen/loco/resource/v1/resource_pb";

import { ALL, filterRows, type RowFilter, statusOptions, viewAllSearch } from "./resourceRows";
import { row } from "./testRows";
import type { EnvResource } from "./useDashboardData";

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

describe("viewAllSearch", () => {
	test("carries the environment and the filters the Resources page reads", () => {
		const search = viewAllSearch("staging", {
			status: "deploying",
			type: "service",
			region: "eu-west-1",
			query: " api ",
		});
		expect(Object.fromEntries(new URLSearchParams(search))).toEqual({
			env: "staging",
			q: "api",
			status: "deploying",
			region: "eu-west-1",
		});
	});

	test("drops a status the Resources page has no filter for", () => {
		expect(viewAllSearch("production", { ...everything, status: "healthy" })).toBe("env=production");
	});
});

describe("statusOptions", () => {
	test("offers only statuses some resource has, plus the selected one", () => {
		const counts = new Map([
			[ResourceStatus.HEALTHY, 3],
			[ResourceStatus.DEGRADED, 1],
		]);
		expect(statusOptions(counts, ALL).map((o) => o.value)).toEqual(["healthy", "degraded"]);
		expect(statusOptions(counts, "unknown").map((o) => o.value)).toEqual(["healthy", "degraded", "unknown"]);
	});
});
