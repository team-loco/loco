const KEY = /^[A-Za-z_][A-Za-z0-9_]*$/;
const KEY_CHAR = /[\p{L}\p{N}_.]/u;
const SPACE = /\s/;
const ESCAPE = /\\./g;
const UNESCAPE = /\\([^$])/g;
const EXPAND = /(\\)?(\$)(\()?\{?([A-Z0-9_]+)?\}?/g;
const EXPORT = "export";

export function isEnvKey(key: string): boolean {
	return KEY.test(key);
}

function expandVariables(value: string, vars: ReadonlyMap<string, string>): string {
	return value.replace(EXPAND, (match: string, escaped?: string, _dollar?: string, paren?: string, name?: string) => {
		if (escaped === "\\" || paren === "(") return match.slice(1);
		if (name !== undefined && name !== "") return vars.get(name) ?? "";
		return match;
	});
}

function expandEscapes(value: string): string {
	const out = value.replace(ESCAPE, (match) => {
		switch (match) {
			case "\\n":
				return "\n";
			case "\\r":
				return "\r";
			default:
				return match;
		}
	});
	return out.replace(UNESCAPE, "$1");
}

function skipToNextLine(src: string, from: number): number {
	const nl = src.indexOf("\n", from);
	return nl === -1 ? src.length : nl + 1;
}

function trimQuotes(value: string, quote: string): string {
	let start = 0;
	let end = value.length;
	while (start < end && value[start] === quote) start++;
	while (end > start && value[end - 1] === quote) end--;
	return value.slice(start, end);
}

function readKey(src: string, from: number): { key: string; next: number } | null {
	let i = from;
	while (i < src.length && SPACE.test(src.charAt(i))) i++;
	if (src.startsWith(EXPORT, i) && SPACE.test(src.charAt(i + EXPORT.length))) {
		i += EXPORT.length;
		while (i < src.length && SPACE.test(src.charAt(i))) i++;
	}
	const start = i;
	for (; i < src.length; i++) {
		const c = src.charAt(i);
		if (c === "=" || c === ":") {
			let next = i + 1;
			while (next < src.length && src.charAt(next) !== "\n" && SPACE.test(src.charAt(next))) next++;
			return { key: src.slice(start, i).trimEnd(), next };
		}
		if (c === "\n" || (!SPACE.test(c) && !KEY_CHAR.test(c))) return null;
	}
	return null;
}

function readUnquoted(src: string, from: number, vars: ReadonlyMap<string, string>): { value: string; next: number } {
	const nl = src.indexOf("\n", from);
	const end = nl === -1 ? src.length : nl;
	const line = src.slice(from, end);
	let cut = line.length;
	for (let i = line.length - 1; i > 0; i--) {
		if (line[i] === "#" && SPACE.test(line.charAt(i - 1))) {
			cut = i;
			break;
		}
	}
	return { value: expandVariables(line.slice(0, cut).trim(), vars), next: end };
}

function readQuoted(
	src: string,
	from: number,
	quote: string,
	vars: ReadonlyMap<string, string>,
): { value: string; next: number } | null {
	for (let i = from + 1; i < src.length; i++) {
		if (src[i] !== quote || src[i - 1] === "\\") continue;
		const inner = trimQuotes(src.slice(from, i), quote);
		const value = quote === '"' ? expandVariables(expandEscapes(inner), vars) : inner;
		return { value, next: i + 1 };
	}
	return null;
}

export function parseDotEnv(text: string): [string, string][] {
	const src = text.replace(/\r\n/g, "\n");
	const vars = new Map<string, string>();
	let pos = 0;
	while (pos < src.length) {
		while (pos < src.length && SPACE.test(src.charAt(pos))) pos++;
		if (pos >= src.length) break;
		if (src[pos] === "#") {
			pos = skipToNextLine(src, pos);
			continue;
		}
		const key = readKey(src, pos);
		if (key === null) {
			pos = skipToNextLine(src, pos);
			continue;
		}
		const quote = src.charAt(key.next);
		const read =
			quote === '"' || quote === "'" ? readQuoted(src, key.next, quote, vars) : readUnquoted(src, key.next, vars);
		if (read === null) {
			pos = skipToNextLine(src, key.next);
			continue;
		}
		vars.set(key.key, read.value);
		pos = read.next;
	}
	return [...vars].filter(([key]) => isEnvKey(key));
}
