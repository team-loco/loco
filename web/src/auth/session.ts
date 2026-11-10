import { readStorage } from "@/lib/storage";

export const TOKENS_KEY = "loco:auth:oidc:v1";

export function hasStoredSession(): boolean {
	return readStorage(TOKENS_KEY) !== null;
}
