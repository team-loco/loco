import { readStorage, removeStorage, writeStorage } from "@/lib/storage";

const NEXT_KEY = "loco:auth:next:v1";

function safe(path: string | null): string | null {
	if (path === null || !path.startsWith("/") || path.startsWith("//")) return null;
	return path;
}

export function rememberNextPath(path: string): void {
	writeStorage(NEXT_KEY, path, "session");
}

export function forgetNextPath(): void {
	removeStorage(NEXT_KEY, "session");
}

export function takeNextPath(): string | null {
	const path = safe(readStorage(NEXT_KEY, "session"));
	forgetNextPath();
	return path;
}
