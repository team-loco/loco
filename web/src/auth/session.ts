import { readStorage } from "@/lib/storage";

export const TOKENS_KEY = "loco:auth:oidc:v1";

const listeners = new Set<() => void>();

export function hasStoredSession(): boolean {
	return readStorage(TOKENS_KEY) !== null;
}

export function subscribeSession(onChange: () => void): () => void {
	listeners.add(onChange);
	return () => {
		listeners.delete(onChange);
	};
}

export function notifySessionChange(): void {
	for (const listener of listeners) listener();
}
