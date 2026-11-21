const KEY = /^[A-Za-z_][A-Za-z0-9_]*$/;

export function isEnvKey(key: string): boolean {
	return KEY.test(key);
}

function unquote(v: string): string {
	if (v.length >= 2 && ((v.startsWith('"') && v.endsWith('"')) || (v.startsWith("'") && v.endsWith("'")))) {
		return v.slice(1, -1);
	}
	return v;
}

function parseLine(raw: string): [string, string] | null {
	const line = raw.trim().replace(/^export\s+/, "");
	if (line === "" || line.startsWith("#")) return null;
	const eq = line.indexOf("=");
	const key = line.slice(0, eq).trim();
	if (eq < 1 || !isEnvKey(key)) return null;
	return [key, unquote(line.slice(eq + 1).trim())];
}

export interface ParsedDotEnv {
	pairs: [string, string][];
	/** 1-based numbers of non-blank, non-comment lines that are not KEY=value. */
	skipped: number[];
	/** Keys that appear on more than one line, in first-seen order. */
	repeated: string[];
}

export function parseDotEnv(text: string): ParsedDotEnv {
	const pairs = new Map<string, string>();
	const skipped: number[] = [];
	const repeated = new Set<string>();
	text.split(/\r?\n/).forEach((raw, index) => {
		const pair = parseLine(raw);
		if (pair === null) {
			const line = raw.trim();
			if (line !== "" && !line.startsWith("#")) skipped.push(index + 1);
			return;
		}
		if (pairs.has(pair[0])) repeated.add(pair[0]);
		pairs.set(...pair);
	});
	return { pairs: [...pairs], skipped, repeated: [...repeated] };
}

/** Sentences telling what an import drops, merges or overwrites, in the order they are shown. */
export function dotEnvNotes({ pairs, skipped, repeated }: ParsedDotEnv, existing: ReadonlySet<string>): string[] {
	const notes: string[] = [];
	if (skipped.length > 0) {
		notes.push(`Skipped ${skipped.length === 1 ? "line" : "lines"} ${skipped.join(", ")} (not KEY=value).`);
	}
	if (repeated.length > 0) {
		notes.push(
			`${repeated.join(", ")} ${repeated.length === 1 ? "appears" : "appear"} more than once; the last value is used.`,
		);
	}
	const replaced = pairs.map(([key]) => key).filter((key) => existing.has(key));
	if (replaced.length > 0) notes.push(`Replaces ${replaced.join(", ")}.`);
	return notes;
}
