import { describe, expect, test } from "bun:test";

import { nextPage, pagedRows, pagerVisible, type Paging } from "./paging";

const ROWS = Array.from({ length: 7 }, (_, i) => i);
const SIZES = [3, 6];

function paged(paging: Paging, all: number[] = ROWS) {
	const moves: Paging[] = [];
	const result = pagedRows(
		all,
		paging,
		(next) => {
			moves.push(next);
		},
		SIZES,
		"none",
	);
	return { result, moves };
}

describe("pagedRows", () => {
	test("slices the page and labels its range", () => {
		const { result } = paged({ page: 1, size: 3 });
		expect(result.rows).toEqual([3, 4, 5]);
		expect(result.pager.label).toBe("4–6 of 7");
	});

	test("clamps a page past the end to the last page", () => {
		const { result } = paged({ page: 9, size: 3 });
		expect(result.rows).toEqual([6]);
		expect(result.pager.onNext).toBeNull();
		expect(result.ends.onLast).toBeNull();
	});

	test("uses the empty label when there are no rows", () => {
		expect(paged({ page: 0, size: 3 }, []).result.pager.label).toBe("none");
	});

	test("moves between pages and returns to the first page on a size change", () => {
		const first = paged({ page: 0, size: 3 });
		expect(first.result.pager.onPrev).toBeNull();
		first.result.ends.onLast?.();
		expect(first.moves).toEqual([{ page: 2, size: 3 }]);
		const middle = paged({ page: 1, size: 3 });
		middle.result.pager.onPageSize(6);
		expect(middle.moves).toEqual([{ page: 0, size: 6 }]);
	});
});

describe("pagerVisible", () => {
	const sizes = [25, 50, 100];

	test.each([
		["one row", 1, false, false],
		["exactly the smallest page size", 25, false, false],
		["one row over the smallest page size", 26, false, true],
		["rows within a larger chosen size", 30, false, true],
		["more server pages", 1, true, true],
	])("%s", (_, total, hasMore, visible) => {
		expect(pagerVisible({ total, pageSizes: sizes, hasMore })).toBe(visible);
	});
});

describe("nextPage", () => {
	const lastLoaded = pagedRows(ROWS, { page: 2, size: 3 }, () => undefined, [3]);
	let loads = 0;
	const loadMore = async () => {
		loads++;
		await Promise.resolve();
		return true;
	};

	test("loads the next server page and moves to it", async () => {
		loads = 0;
		const moves: Paging[] = [];
		const next = nextPage(lastLoaded, false, true, loadMore, (update) => {
			if (typeof update === "function") moves.push(update({ page: 2, size: 3 }));
		});
		next?.();
		await Bun.sleep(0);
		expect(loads).toBe(1);
		expect(moves).toEqual([{ page: 3, size: 3 }]);
	});

	test("stays enabled but ignores a press while a page is loading", () => {
		loads = 0;
		const next = nextPage(lastLoaded, true, true, loadMore, () => undefined);
		expect(next).not.toBeNull();
		next?.();
		expect(loads).toBe(0);
	});
});
