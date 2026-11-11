import { describe, expect, test } from "bun:test";

import { pageSelection, setPage, toggleId } from "./selection";

describe("selection", () => {
	test("counts only rows on the page", () => {
		const sel = pageSelection(new Set(["a", "z"]), ["a", "b"]);
		expect(sel).toEqual({ count: 1, all: false, some: true });
	});

	test("an empty page is never fully selected", () => {
		expect(pageSelection(new Set(["a"]), []).all).toBe(false);
	});

	test("toggleId adds and removes", () => {
		expect([...toggleId(new Set(["a"]), "b")]).toEqual(["a", "b"]);
		expect([...toggleId(new Set(["a"]), "a")]).toEqual([]);
	});

	test("setPage adds or removes every row on the page and keeps the rest", () => {
		expect([...setPage(new Set(["a", "z"]), ["a", "b"], true)].toSorted()).toEqual(["a", "b", "z"]);
		expect([...setPage(new Set(["a", "z"]), ["a", "b"], false)]).toEqual(["z"]);
	});
});
