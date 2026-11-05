const incompleteList = new Error("Could not load the complete list");

export async function collectPages<T>(
	loadPage: (pageToken: string) => Promise<{ items: T[]; nextPageToken: string }>,
): Promise<T[]> {
	const items: T[] = [];
	const seen = new Set<string>();
	let pageToken = "";
	do {
		const page = await loadPage(pageToken);
		items.push(...page.items);
		pageToken = page.nextPageToken;
		if (pageToken !== "" && seen.has(pageToken)) throw incompleteList;
		seen.add(pageToken);
	} while (pageToken !== "");
	return items;
}
