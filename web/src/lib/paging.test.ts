import { describe, expect, test } from "bun:test";

import { pagedRows, type Paging } from "./paging";

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
		middle.result.pager.onPageSize?.(6);
		expect(middle.moves).toEqual([{ page: 0, size: 6 }]);
	});

	test("offers no size choice for a single page size", () => {
		const result = pagedRows(ROWS, { page: 0, size: 3 }, () => undefined, [3]);
		expect(result.pager.pageSizes).toBeUndefined();
	});
});
