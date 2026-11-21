import { createParser, parseAsArrayOf, parseAsNumberLiteral, parseAsString, useQueryStates } from "nuqs";
import { useMemo, type Dispatch, type SetStateAction } from "react";

import { formatCount } from "@/lib/format";

// A zero-based page index, written to the URL counting from one.
export const pageParser = createParser<number>({
	parse: (raw) => {
		const n = Number(raw);
		return Number.isSafeInteger(n) && n > 0 ? n - 1 : null;
	},
	serialize: (page) => String(page + 1),
});

export function pagingParsers(sizes: readonly number[], defaultSize: number) {
	return { page: pageParser.withDefault(0), size: parseAsNumberLiteral(sizes).withDefault(defaultSize) };
}

export function pageSlice<T>(rows: T[], page: number, size: number): { rows: T[]; page: number; pages: number } {
	const pages = Math.max(1, Math.ceil(rows.length / size));
	const p = Math.min(page, pages - 1);
	return { rows: rows.slice(p * size, p * size + size), page: p, pages };
}

export function pageRangeLabel(page: number, size: number, total: number): string {
	const first = page * size + 1;
	const last = Math.min(total, (page + 1) * size);
	return `${first.toString()}–${last.toString()} of ${total.toString()}`;
}

export interface Paging {
	page: number;
	size: number;
}

export interface PagedRows<T> {
	rows: T[];
	pager: {
		label: string;
		page: number;
		total: number;
		pageSizes: readonly number[];
		pageSize: number;
		onPageSize: (size: number) => void;
		onPrev: (() => void) | null;
		onNext: (() => void) | null;
	};
	ends: { onFirst: (() => void) | null };
}

export function pagedRows<T>(
	all: T[],
	paging: Paging,
	setPaging: (next: Paging) => void,
	sizes: readonly number[],
	empty = "0 of 0",
): PagedRows<T> {
	const { size } = paging;
	const { rows, page, pages } = pageSlice(all, paging.page, size);
	const go = (target: number) => () => {
		setPaging({ page: target, size });
	};
	return {
		rows,
		pager: {
			label: all.length > 0 ? pageRangeLabel(page, size, all.length) : empty,
			page,
			total: all.length,
			pageSizes: sizes,
			pageSize: size,
			onPageSize: (next: number) => {
				setPaging({ page: 0, size: next });
			},
			onPrev: page > 0 ? go(page - 1) : null,
			onNext: page < pages - 1 ? go(page + 1) : null,
		},
		ends: {
			onFirst: page > 0 ? go(0) : null,
		},
	};
}

/** Pages rows held in memory, keeping the page and size in the URL. */
export function usePagedRows<T>(
	all: T[],
	sizes: readonly [number, ...number[]],
	empty?: string,
): PagedRows<T> & { reset: () => void } {
	const parsers = useMemo(() => pagingParsers(sizes, sizes[0]), [sizes]);
	const [paging, setPaging] = useQueryStates(parsers, { history: "replace" });
	return {
		...pagedRows(
			all,
			paging,
			(next) => {
				void setPaging(next);
			},
			sizes,
			empty,
		),
		reset: () => {
			void setPaging({ page: null });
		},
	};
}

export function nextPage(
	rows: PagedRows<unknown>,
	isLoading: boolean,
	hasMore: boolean,
	loadMore: () => Promise<boolean>,
	setPaging: Dispatch<SetStateAction<Paging>>,
): (() => void) | null {
	if (rows.pager.onNext !== null || !hasMore) return rows.pager.onNext;
	return () => {
		// Disabling the button while a page loads would drop its keyboard focus.
		if (isLoading) return;
		void loadMore().then((loaded) => {
			if (loaded) setPaging((current) => ({ ...current, page: current.page + 1 }));
		});
	};
}

/** The page tokens a list followed from its first page; the last one loads the current page. */
export const pageTokensParser = parseAsArrayOf(parseAsString).withDefault([]);

// The API counts at most this many rows exactly; a total_count above it means more.
const TOTAL_COUNT_CAP = 10_000;

/** "51–100", or "51–100 of 1,234" and "of 10,000+" when the API returned a total. */
export function tokenPageLabel(page: number, size: number, rows: number, total: number | undefined): string {
	if (rows === 0) return "0";
	const range = `${formatCount(page * size + 1)}–${formatCount(page * size + rows)}`;
	return total === undefined ? range : `${range} of ${formatTotal(total)}`;
}

/** A total_count from the API: "1,234", or "10,000+" past what the API counts exactly. */
export function formatTotal(total: number): string {
	return total > TOTAL_COUNT_CAP ? `${formatCount(TOTAL_COUNT_CAP)}+` : formatCount(total);
}

/** Pager moves over page tokens: Next pushes nextToken, Previous pops the last token, First clears them. */
export function tokenPager(tokens: readonly string[], nextToken: string, go: (tokens: string[] | null) => void) {
	return {
		page: tokens.length,
		onFirst:
			tokens.length > 0
				? () => {
						go(null);
					}
				: null,
		onPrev:
			tokens.length > 0
				? () => {
						go(tokens.length > 1 ? tokens.slice(0, -1) : null);
					}
				: null,
		onNext:
			nextToken !== ""
				? () => {
						go([...tokens, nextToken]);
					}
				: null,
	};
}
