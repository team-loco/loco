import { describe, expect, test } from "bun:test";

import { pageSelection, toggleId, togglePage } from "./selection";

describe("selection", () => {
	test("counts only rows on the page", () => {
		const sel = pageSelection(new Set(["a", "z"]), ["a", "b"]);
		expect(sel).toEqual({ all: false, some: true });
	});

	test("an empty page is never fully selected", () => {
		expect(pageSelection(new Set(["a"]), []).all).toBe(false);
	});

	test("toggleId adds and removes", () => {
		expect([...toggleId(new Set(["a"]), "b")]).toEqual(["a", "b"]);
		expect([...toggleId(new Set(["a"]), "a")]).toEqual([]);
	});

	test("togglePage selects a partly selected page and clears a full one", () => {
		const full = togglePage(new Set(["a"]), ["a", "b"]);
		expect([...full].toSorted()).toEqual(["a", "b"]);
		expect([...togglePage(new Set(["a", "b", "z"]), ["a", "b"])]).toEqual(["z"]);
	});
});
