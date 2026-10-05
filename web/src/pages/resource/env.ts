export type EnvPair = [string, string];

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

export function parseDotEnv(text: string): EnvPair[] {
	const out: EnvPair[] = [];
	for (const raw of text.split(/\r?\n/)) {
		const line = raw.trim().replace(/^export\s+/, "");
		if (line === "" || line.startsWith("#")) continue;
		const eq = line.indexOf("=");
		if (eq < 1) continue;
		const key = line.slice(0, eq).trim();
		if (!isEnvKey(key)) continue;
		const value = unquote(line.slice(eq + 1).trim());
		out.push([key, value]);
	}
	return out;
}

export function mergeEnv(base: EnvPair[], incoming: EnvPair[]): EnvPair[] {
	const map = new Map<string, string>(base);
	for (const [k, v] of incoming) map.set(k, v);
	return [...map.entries()].filter(([k]) => k !== "");
}

export function validateEnv(pairs: EnvPair[]): string | undefined {
	if (pairs.length === 0) return "Add at least one variable.";
	const seen = new Set<string>();
	for (const [k] of pairs) {
		if (k === "") return "Every variable needs a name.";
		if (!isEnvKey(k)) return `${k} is not a valid name. Use letters, digits and underscores, not starting with a digit.`;
		if (seen.has(k)) return `${k} is defined more than once.`;
		seen.add(k);
	}
	return undefined;
}
