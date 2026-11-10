export function pageSelection(selected: ReadonlySet<string>, pageIds: readonly string[]) {
	const ids = pageIds.filter((id) => selected.has(id));
	return { ids, all: pageIds.length > 0 && ids.length === pageIds.length, some: ids.length > 0 };
}

export function toggleId(selected: ReadonlySet<string>, id: string): Set<string> {
	const next = new Set(selected);
	if (next.has(id)) next.delete(id);
	else next.add(id);
	return next;
}

export function togglePage(selected: ReadonlySet<string>, pageIds: readonly string[]): Set<string> {
	const next = new Set(selected);
	const clear = pageSelection(selected, pageIds).all;
	for (const id of pageIds) {
		if (clear) next.delete(id);
		else next.add(id);
	}
	return next;
}
