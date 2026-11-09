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

export type DotEnvLine = { kind: "blank" } | { kind: "invalid" } | { kind: "pair"; key: string; value: string };

export function parseDotEnvLine(raw: string): DotEnvLine {
	const line = raw.trim().replace(/^export\s+/, "");
	if (line === "" || line.startsWith("#")) return { kind: "blank" };
	const eq = line.indexOf("=");
	const key = line.slice(0, eq).trim();
	if (eq < 1 || !isEnvKey(key)) return { kind: "invalid" };
	return { kind: "pair", key, value: unquote(line.slice(eq + 1).trim()) };
}

export function parseDotEnv(text: string): [string, string][] {
	const out: [string, string][] = [];
	for (const raw of text.split(/\r?\n/)) {
		const line = parseDotEnvLine(raw);
		if (line.kind === "pair") out.push([line.key, line.value]);
	}
	return out;
}
