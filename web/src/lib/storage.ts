import { useCallback, useSyncExternalStore } from "react";

type StorageArea = "local" | "session";

function storageFor(area: StorageArea): Storage {
	return area === "local" ? window.localStorage : window.sessionStorage;
}

export function readStorage(key: string, area: StorageArea = "local"): string | null {
	try {
		return storageFor(area).getItem(key);
	} catch {
		return null;
	}
}

export function writeStorage(key: string, value: string, area: StorageArea = "local"): boolean {
	try {
		storageFor(area).setItem(key, value);
		return true;
	} catch {
		return false;
	}
}

export function removeStorage(key: string, area: StorageArea = "local"): void {
	try {
		storageFor(area).removeItem(key);
	} catch {
		return;
	}
}

const listeners = new Set<() => void>();

function subscribe(listener: () => void): () => void {
	listeners.add(listener);
	window.addEventListener("storage", listener);
	return () => {
		listeners.delete(listener);
		window.removeEventListener("storage", listener);
	};
}

function parseJSON(raw: string | null): unknown {
	if (raw === null) return null;
	try {
		return JSON.parse(raw);
	} catch {
		return null;
	}
}

export function useStoredJSON<T>(
	key: string | null,
	parse: (value: unknown) => T,
): [T, (fn: (current: T) => T | null) => boolean] {
	const raw = useSyncExternalStore(subscribe, () => (key === null ? null : readStorage(key)));
	const update = useCallback(
		(fn: (current: T) => T | null): boolean => {
			if (key === null) return false;
			const next = fn(parse(parseJSON(readStorage(key))));
			let saved = true;
			if (next === null) removeStorage(key);
			else saved = writeStorage(key, JSON.stringify(next));
			for (const listener of listeners) listener();
			return saved;
		},
		[key, parse],
	);
	return [parse(parseJSON(raw)), update];
}
