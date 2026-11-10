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

export function parseDotEnv(text: string): [string, string][] {
	const pairs = new Map<string, string>();
	for (const raw of text.split(/\r?\n/)) {
		const pair = parseLine(raw);
		if (pair !== null) pairs.set(...pair);
	}
	return [...pairs];
}
