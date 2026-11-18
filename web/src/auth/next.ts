const STATE_KEY = "next";

export function safeNextPath(path: unknown): string | null {
	if (typeof path !== "string" || !path.startsWith("/") || path.startsWith("//") || path.includes("\\")) return null;
	for (const ch of path) {
		if (ch <= "\u001f" || ch === "\u007f") return null;
	}
	return path;
}

export function nextPathState(path: string): Record<typeof STATE_KEY, string> {
	return { [STATE_KEY]: path };
}

export function nextPathFrom(state: unknown): string | null {
	if (typeof state !== "object" || state === null || !(STATE_KEY in state)) return null;
	return safeNextPath(state[STATE_KEY]);
}
