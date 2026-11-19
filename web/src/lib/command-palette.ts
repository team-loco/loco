const WORDS = /[\s\-_./:]+/;
const PREFIX_SCORE = 1;
const MATCH_SCORE = 0.5;
const NO_MATCH = 0;

export function scorePaletteItem(_value: string, search: string, keywords: string[] = []): number {
	const text = keywords.join(" ").toLowerCase();
	const query = search.trim().toLowerCase();
	const textWords = text.split(WORDS);
	if (!query.split(WORDS).every((word) => textWords.some((candidate) => candidate.startsWith(word)))) return NO_MATCH;
	return text.startsWith(query) ? PREFIX_SCORE : MATCH_SCORE;
}
