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
	viewAllSearch,
} from "./resourceRows";
import { visibleStatusFilters } from "@/pages/resources/table";

import { COLUMNS } from "./ResourcesTable";
import { draft, row } from "./testRows";

const everything: RowFilter = { status: ALL, type: ALL, region: ALL, query: "" };

function names(rows: DashboardItem[]): string[] {
	return rows.map(itemName);
}

describe("filterRows", () => {
	const rows = [
		row("api", { description: "Public storefront API", domain: "api.example.dev" }),
		row("worker", { status: ResourceStatus.UNAVAILABLE, regions: [{ region: "us-ca-1", desired: 2, ready: 0 }] }),
		row("billing", { status: ResourceStatus.DEPLOYING }),
	];

	test("search matches the description, the domain and the region", () => {
		expect(names(filterRows(rows, { ...everything, query: "storefront" }))).toEqual(["api"]);
		expect(names(filterRows(rows, { ...everything, query: "example.dev" }))).toEqual(["api"]);
		expect(names(filterRows(rows, { ...everything, query: "US-CA" }))).toEqual(["worker"]);
	});

	test("a status filter keeps only that status and All keeps every row", () => {
		expect(names(filterRows(rows, { ...everything, status: "unavailable" }))).toEqual(["worker"]);
		expect(names(filterRows(rows, { ...everything, status: "healthy" }))).toEqual(["api"]);
		expect(names(filterRows(rows, everything))).toEqual(["api", "billing", "worker"]);
	});
});

describe("draft and not deployed filters", () => {
	const never = { ...row("never"), neverDeployed: true };
	const rows = [row("api"), never, ...draftItems([draft("web")], [], "")];

	test("Not deployed keeps created resources without a deployment and Draft keeps browser drafts", () => {
		expect(names(filterRows(rows, { ...everything, status: "not-deployed" }))).toEqual(["never"]);
		expect(names(filterRows(rows, { ...everything, status: "draft" }))).toEqual(["web"]);
		expect(names(filterRows(rows, { ...everything, status: "healthy" }))).toEqual(["api"]);
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
	const west = [{ region: "us-ca-1", desired: 1, ready: 1 }];
	const items = [row("c", { regions: west }), row("a", { regions: west }), row("b"), row("d", { regions: west })];

	test("ties sort by name ascending in either direction", () => {
		expect(names(sortRows(items, { id: "regions", desc: false }))).toEqual(["a", "c", "d", "b"]);
		expect(names(sortRows(items, { id: "regions", desc: true }))).toEqual(["b", "a", "c", "d"]);
		expect(names(sortRows(items, { id: "deploy", desc: false }))).toEqual(["a", "b", "c", "d"]);
	});
});

describe("draftItems", () => {
	const regions = [create(RegionInfoSchema, { region: "us-va-1" }), create(RegionInfoSchema, { region: "us-ca-1" })];
	const items = draftItems(
		[draft("web"), draft("queue", { isPublic: false, regions: ["us-ca-1"] })],
		regions,
		"us-va-1",
	);

	test("place a draft in its regions with nothing running", () => {
		expect(items.map((i) => [i.draft.name, itemIsPublic(i), i.regions])).toEqual([
			["web", true, [{ region: "us-va-1", desired: 0, ready: 0 }]],
			["queue", false, [{ region: "us-ca-1", desired: 0, ready: 0 }]],
		]);
	});

	test("match the search, type and region filters but no status", () => {
		const rows: DashboardItem[] = [row("api"), ...items];
		expect(names(filterRows(rows, { ...everything, query: "acme/queue" }))).toEqual(["queue"]);
		expect(names(filterRows(rows, { ...everything, type: "service", region: "us-ca-1" }))).toEqual(["queue"]);
		expect(names(filterRows(rows, { ...everything, type: "database" }))).toEqual([]);
		expect(names(filterRows(rows, { ...everything, status: "healthy" }))).toEqual(["api"]);
	});
});

describe("viewAllSearch", () => {
	test("carries the environment and the filters the Resources page reads", () => {
		const search = viewAllSearch("staging", {
			status: "deploying",
			type: "service",
			region: "us-ca-1",
			query: " api ",
		});
		expect(Object.fromEntries(new URLSearchParams(search))).toEqual({
			env: "staging",
			q: "api",
			status: "deploying",
			region: "us-ca-1",
		});
	});

	test("carries every status filter, as both pages share them", () => {
		expect(viewAllSearch("production", { ...everything, status: "healthy" })).toBe("?env=production&status=healthy");
		expect(viewAllSearch("production", { ...everything, status: "draft" })).toBe("?env=production&status=draft");
	});
});

describe("visibleStatusFilters", () => {
	test("offers only statuses some resource has, plus the selected one", () => {
		const counts = new Map([
			["healthy", 3],
			["degraded", 1],
		]);
		expect(visibleStatusFilters(counts, ALL).map((o) => o.value)).toEqual(["healthy", "degraded"]);
		expect(visibleStatusFilters(counts, "unknown").map((o) => o.value)).toEqual(["healthy", "degraded", "unknown"]);
	});
});
