const countFormat = new Intl.NumberFormat();

export function formatCount(n: number): string {
	return countFormat.format(n);
}

export function pluralize(n: number, word: string): string {
	return `${n.toString()} ${word}${n === 1 ? "" : "s"}`;
}
