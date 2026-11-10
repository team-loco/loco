const WORDS = /\s+/;
const PREFIX_SCORE = 1;
const MATCH_SCORE = 0.5;
const NO_MATCH = 0;

export function scorePaletteItem(_value: string, search: string, keywords: string[] = []): number {
	const text = keywords.join(" ").toLowerCase();
	const query = search.trim().toLowerCase();
	if (!query.split(WORDS).every((word) => text.includes(word))) return NO_MATCH;
	return text.startsWith(query) ? PREFIX_SCORE : MATCH_SCORE;
}
