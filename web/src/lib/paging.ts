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
