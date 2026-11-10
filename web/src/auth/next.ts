import { readStorage, removeStorage, writeStorage } from "@/lib/storage";

const NEXT_KEY = "loco:auth:next:v1";
const STATE_KEY = "next";

export function safeNextPath(path: unknown): string | null {
	if (typeof path !== "string" || !path.startsWith("/") || path.startsWith("//") || path.includes("\\")) return null;
	return path;
}

export function nextPathState(path: string): Record<typeof STATE_KEY, string> {
	return { [STATE_KEY]: path };
}

export function nextPathFrom(state: unknown): string | null {
	if (typeof state !== "object" || state === null || !(STATE_KEY in state)) return null;
	return safeNextPath(state[STATE_KEY]);
}

export function rememberNextPath(path: string): void {
	writeStorage(NEXT_KEY, path, "session");
}

export function forgetNextPath(): void {
	removeStorage(NEXT_KEY, "session");
}

export function takeNextPath(): string | null {
	const path = safeNextPath(readStorage(NEXT_KEY, "session"));
	forgetNextPath();
	return path;
}
