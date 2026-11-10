import { useState, type Dispatch, type SetStateAction } from "react";

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
		pageSizes?: readonly number[];
		pageSize?: number;
		onPageSize?: (size: number) => void;
		onPrev: (() => void) | null;
		onNext: (() => void) | null;
	};
	ends: { onFirst: (() => void) | null; onLast: (() => void) | null };
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
	const sizeControls =
		sizes.length > 1
			? {
					pageSizes: sizes,
					pageSize: size,
					onPageSize: (next: number) => {
						setPaging({ page: 0, size: next });
					},
				}
			: {};
	return {
		rows,
		pager: {
			label: all.length > 0 ? pageRangeLabel(page, size, all.length) : empty,
			...sizeControls,
			onPrev: page > 0 ? go(page - 1) : null,
			onNext: page < pages - 1 ? go(page + 1) : null,
		},
		ends: {
			onFirst: page > 0 ? go(0) : null,
			onLast: page < pages - 1 ? go(pages - 1) : null,
		},
	};
}

export function usePagedRows<T>(
	all: T[],
	sizes: readonly [number, ...number[]],
	empty?: string,
): PagedRows<T> & { reset: () => void } {
	const [paging, setPaging] = useState<Paging>({ page: 0, size: sizes[0] });
	return {
		...pagedRows(all, paging, setPaging, sizes, empty),
		reset: () => {
			setPaging((prev) => ({ ...prev, page: 0 }));
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
