export function pageSelection(selected: ReadonlySet<string>, pageIds: readonly string[]) {
	const count = pageIds.filter((id) => selected.has(id)).length;
	return { count, all: pageIds.length > 0 && count === pageIds.length, some: count > 0 };
}

export function toggleId(selected: ReadonlySet<string>, id: string): Set<string> {
	const next = new Set(selected);
	if (next.has(id)) next.delete(id);
	else next.add(id);
	return next;
}

export function setPage(selected: ReadonlySet<string>, pageIds: readonly string[], on: boolean): Set<string> {
	const next = new Set(selected);
	for (const id of pageIds) {
		if (on) next.add(id);
		else next.delete(id);
	}
	return next;
}

export function togglePage(selected: ReadonlySet<string>, pageIds: readonly string[]): Set<string> {
	return setPage(selected, pageIds, !pageSelection(selected, pageIds).all);
}
