const PALETTE_GROUPS = ["Actions", "Resources", "Pages", "Workspaces"] as const;

type PaletteGroup = (typeof PALETTE_GROUPS)[number];

export interface PaletteEntry {
	id: string;
	group: PaletteGroup;
	label: string;
	keywords?: string;
}

const BROWSE_LIMIT = 8;
const SEARCH_LIMIT = 6;
const WORDS = /\s+/;

function haystack(entry: PaletteEntry): string {
	return `${entry.label} ${entry.keywords ?? ""}`.toLowerCase();
}

export function filterPalette<T extends PaletteEntry>(
	entries: readonly T[],
	query: string,
): { group: PaletteGroup; items: T[] }[] {
	const q = query.trim().toLowerCase();
	const words = q.split(WORDS).filter(Boolean);
	const matches =
		q === ""
			? entries.filter((e) => e.group !== "Workspaces")
			: entries.filter((e) => {
					const text = haystack(e);
					return words.every((w) => text.includes(w));
				});
	const ranked =
		q === ""
			? matches
			: matches.toSorted(
					(a, b) => Number(b.label.toLowerCase().startsWith(q)) - Number(a.label.toLowerCase().startsWith(q)),
				);
	const limit = q === "" ? BROWSE_LIMIT : SEARCH_LIMIT;
	return PALETTE_GROUPS.map((group) => ({
		group,
		items: ranked.filter((e) => e.group === group).slice(0, limit),
	})).filter((g) => g.items.length > 0);
}
