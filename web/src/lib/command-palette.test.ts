import { describe, expect, test } from "bun:test";

import { scorePaletteItem } from "./command-palette";

const score = (search: string, ...keywords: string[]) => scorePaletteItem("item-id", search, keywords);

describe("scorePaletteItem", () => {
	test("matches the start of a word of the label or keywords, ignoring case", () => {
		expect(score("PAYMENTS", "billing-api", "payments service")).toBeGreaterThan(0);
		expect(score("store", "Switch to storefront")).toBeGreaterThan(0);
		expect(score("cat", "product-catalog")).toBeGreaterThan(0);
	});

	test("does not match the middle of a word", () => {
		expect(score("log", "product-catalog", "catalog")).toBe(0);
	});

	test("matches a query split differently from the item, scoring below a word match", () => {
		expect(score("front end", "frontend-proxy")).toBeGreaterThan(0);
		expect(score("front end", "frontend-proxy")).toBeLessThan(score("front", "frontend-proxy"));
		expect(score("front-end prox", "frontend proxy")).toBeGreaterThan(0);
		expect(score("end pro", "frontend-proxy")).toBe(0);
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
