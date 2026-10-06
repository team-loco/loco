
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
