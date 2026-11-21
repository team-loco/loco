import { describe, expect, test } from "bun:test";
import { create } from "@bufbuild/protobuf";
import { RegionInfoSchema } from "@gen/loco/resource/v1/resource_pb";

import { ALL, draftItems, itemIsPublic, type RowFilter, viewAllSearch } from "./resourceRows";
import { visibleStatusFilters } from "@/pages/resources/table";

import { draft } from "./testRows";

const everything: RowFilter = { status: ALL, type: ALL, region: ALL, query: "" };

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
