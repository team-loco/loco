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

export function parseDotEnv(text: string): [string, string][] {
	const out: [string, string][] = [];
	for (const raw of text.split(/\r?\n/)) {
		const line = raw.trim().replace(/^export\s+/, "");
		if (line === "" || line.startsWith("#")) continue;
		const eq = line.indexOf("=");
		if (eq < 1) continue;
		const key = line.slice(0, eq).trim();
		if (!isEnvKey(key)) continue;
		out.push([key, unquote(line.slice(eq + 1).trim())]);
	}
	return out;
}
