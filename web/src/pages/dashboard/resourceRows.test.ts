import { describe, expect, test } from "bun:test";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { useTable, type ColumnSort } from "@tanstack/react-table";
import { ResourceStatus } from "@gen/loco/resource/v1/resource_pb";

import { dataTableFeatures, sortOptions } from "@/components/design/DataTable";

import { create } from "@bufbuild/protobuf";
import { RegionInfoSchema } from "@gen/loco/resource/v1/resource_pb";

import {
	ALL,
	draftItems,
	itemIsPublic,
	filterRows,
	type DashboardItem,
	itemName,
	type RowFilter,
	statusOptions,
	viewAllSearch,
} from "./resourceRows";
import { COLUMNS } from "./ResourcesTable";
import { draft, row } from "./testRows";

const everything: RowFilter = { status: ALL, type: ALL, region: ALL, query: "" };

function names(rows: DashboardItem[]): string[] {
	return rows.map(itemName);
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
		expect(names(filterRows(rows, everything))).toEqual(["api", "billing", "worker"]);
	});
});

describe("table sort", () => {
	function sortRows(items: DashboardItem[], sorting: ColumnSort): DashboardItem[] {
		let sorted: DashboardItem[] = [];
		function Rows() {
			const table = useTable({
				...sortOptions,
				features: dataTableFeatures,
				columns: COLUMNS,
				data: filterRows(items, everything),
				state: { sorting: [sorting], pagination: { pageIndex: 0, pageSize: items.length } },
			});
			sorted = table.getRowModel().rows.map((r) => r.original);
			return null;
		}
		renderToStaticMarkup(createElement(Rows));
		return sorted;
	}
	const eu = [{ region: "eu-west-1", desired: 1, ready: 1 }];
	const items = [row("c", { regions: eu }), row("a", { regions: eu }), row("b"), row("d", { regions: eu })];

	test("ties sort by name ascending in either direction", () => {
		expect(names(sortRows(items, { id: "regions", desc: false }))).toEqual(["a", "c", "d", "b"]);
		expect(names(sortRows(items, { id: "regions", desc: true }))).toEqual(["b", "a", "c", "d"]);
		expect(names(sortRows(items, { id: "deploy", desc: false }))).toEqual(["a", "b", "c", "d"]);
	});
});

describe("draftItems", () => {
	const regions = [
		create(RegionInfoSchema, { region: "us-east-1", isDefault: true }),
		create(RegionInfoSchema, { region: "eu-west-1" }),
	];
	const items = draftItems([draft("web"), draft("queue", { isPublic: false, regions: ["eu-west-1"] })], regions);

	test("place a draft in its regions with nothing running", () => {
		expect(items.map((i) => [i.draft.name, itemIsPublic(i), i.regions])).toEqual([
			["web", true, [{ region: "us-east-1", desired: 0, ready: 0 }]],
			["queue", false, [{ region: "eu-west-1", desired: 0, ready: 0 }]],
		]);
	});

	test("match the search, type and region filters but no status", () => {
		const rows: DashboardItem[] = [row("api"), ...items];
		expect(names(filterRows(rows, { ...everything, query: "acme/queue" }))).toEqual(["queue"]);
		expect(names(filterRows(rows, { ...everything, type: "service", region: "eu-west-1" }))).toEqual(["queue"]);
		expect(names(filterRows(rows, { ...everything, type: "database" }))).toEqual([]);
		expect(names(filterRows(rows, { ...everything, status: "healthy" }))).toEqual(["api"]);
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
