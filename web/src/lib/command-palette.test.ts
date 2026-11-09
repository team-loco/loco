import { describe, expect, test } from "bun:test";

import { filterPalette, type PaletteEntry } from "./command-palette";

const entries: PaletteEntry[] = [
	{ id: "p-logs", group: "Pages", label: "Logs" },
	{ id: "p-dash", group: "Pages", label: "Dashboard" },
	{ id: "r-api", group: "Resources", label: "billing-api", keywords: "payments service" },
	{ id: "r-logs", group: "Resources", label: "log-shipper" },
	{ id: "a-new", group: "Actions", label: "New resource", keywords: "create deploy" },
	{ id: "w-prod", group: "Workspaces", label: "Switch to storefront" },
];

function ids(query: string): string[] {
	return filterPalette(entries, query).flatMap((g) => g.items.map((i) => i.id));
}

describe("filterPalette", () => {
	test("an empty query lists every group in order except workspaces", () => {
		const groups = filterPalette(entries, "").map((g) => g.group);
		expect(groups).toEqual(["Actions", "Resources", "Pages"]);
	});

	test("matches a substring of the label or keywords, ignoring case", () => {
		expect(ids("PAYMENTS")).toEqual(["r-api"]);
		expect(ids("store")).toEqual(["w-prod"]);
	});

	test("every word of the query must match", () => {
		expect(ids("create resource")).toEqual(["a-new"]);
		expect(ids("create logs")).toEqual([]);
	});

	test("labels starting with the query rank first within a group", () => {
		const resources = filterPalette(entries, "log").find((g) => g.group === "Resources");
		expect(resources?.items.map((i) => i.id)).toEqual(["r-logs"]);
		const pages = filterPalette(
			[
				{ id: "p-audit", group: "Pages", label: "Audit log" },
				{ id: "p-logs", group: "Pages", label: "Logs" },
			],
			"log",
		);
		expect(pages[0]?.items.map((i) => i.id)).toEqual(["p-logs", "p-audit"]);
	});

	test("caps each group", () => {
		const many: PaletteEntry[] = Array.from({ length: 20 }, (_, i) => ({
			id: `r${String(i)}`,
			group: "Resources",
			label: `svc-${String(i)}`,
		}));
		expect(filterPalette(many, "")[0]?.items.length).toBeLessThan(many.length);
		expect(filterPalette(many, "svc")[0]?.items.length).toBeLessThan(many.length);
	});
});
