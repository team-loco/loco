const KEY = "signInError";

export function signInErrorState(message: string): Record<typeof KEY, string> {
	return { [KEY]: message };
}

export function signInError(state: unknown): string | null {
	if (typeof state !== "object" || state === null || !(KEY in state)) return null;
	const message: unknown = state[KEY];
	return typeof message === "string" ? message : null;
}
