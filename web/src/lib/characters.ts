/** The number of characters in `text`; an emoji counts once. */
export function charCount(text: string): number {
	return Array.from(text).length;
}

/** `text` cut to at most `max` characters. */
export function clampChars(text: string, max: number): string {
	return Array.from(text).slice(0, max).join("");
}
