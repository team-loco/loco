import { describe, expect, test } from "bun:test";

import { scorePaletteItem } from "./command-palette";

const score = (search: string, ...keywords: string[]) => scorePaletteItem("item-id", search, keywords);

describe("scorePaletteItem", () => {
	test("matches a substring of the label or keywords, ignoring case", () => {
		expect(score("PAYMENTS", "billing-api", "payments service")).toBeGreaterThan(0);
		expect(score("store", "Switch to storefront")).toBeGreaterThan(0);
	});

	test("does not match letters scattered across words", () => {
		expect(score("log", "Light mode", "appearance theme mode")).toBe(0);
	});

	test("ignores the item value", () => {
		expect(score("item", "Logs")).toBe(0);
	});

	test("every word of the query must match", () => {
		expect(score("create resource", "New resource", "create deploy")).toBeGreaterThan(0);
		expect(score("create logs", "New resource", "create deploy")).toBe(0);
	});

	test("labels starting with the query rank above other matches", () => {
		expect(score("log", "Logs")).toBeGreaterThan(score("log", "Audit log"));
	});
});
