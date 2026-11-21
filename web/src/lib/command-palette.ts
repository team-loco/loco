const WORDS = /[\s\-_./:]+/;
const PREFIX_SCORE = 1;
const MATCH_SCORE = 0.5;
const JOINED_SCORE = 0.25;
const NO_MATCH = 0;

export function scorePaletteItem(_value: string, search: string, keywords: string[] = []): number {
	const text = keywords.join(" ").toLowerCase();
	const query = search.trim().toLowerCase();
	const textWords = text.split(WORDS);
	if (!query.split(WORDS).every((word) => textWords.some((candidate) => candidate.startsWith(word)))) {
		return matchesJoined(textWords, query) ? JOINED_SCORE : NO_MATCH;
	}
	return text.startsWith(query) ? PREFIX_SCORE : MATCH_SCORE;
}

// A query typed with different separators than the item ("front end" for "frontend-proxy") matches from a word start.
function matchesJoined(textWords: string[], query: string): boolean {
	const joined = query.split(WORDS).join("");
	return textWords.some((_, i) => textWords.slice(i).join("").startsWith(joined));
}
