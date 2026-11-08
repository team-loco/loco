import { readStorage, removeStorage, writeStorage } from "./storage";

const modes = ["system", "light", "dark"] as const;
export type Mode = (typeof modes)[number];
export type ResolvedTheme = Exclude<Mode, "system">;

const modeKey = "loco:mode:v1";
const darkQuery = "(prefers-color-scheme: dark)";

export function parseMode(value: string | null): Mode {
	return modes.find((mode) => mode === value) ?? "system";
}

export function resolveMode(mode: Mode, systemDark: boolean): ResolvedTheme {
	if (mode === "system") {
		return systemDark ? "dark" : "light";
	}
	return mode;
}

export function toggledMode(mode: Mode, systemDark: boolean): Mode {
	const next: ResolvedTheme = resolveMode(mode, systemDark) === "dark" ? "light" : "dark";
	return next === resolveMode("system", systemDark) ? "system" : next;
}

export function readMode(): Mode {
	return parseMode(readStorage(modeKey));
}

export function storeMode(mode: Mode): void {
	if (mode === "system") {
		removeStorage(modeKey);
		return;
	}
	writeStorage(modeKey, mode);
}

export function systemPrefersDark(): boolean {
	return window.matchMedia(darkQuery).matches;
}

export function subscribeSystemTheme(onChange: () => void): () => void {
	const query = window.matchMedia(darkQuery);
	query.addEventListener("change", onChange);
	return () => {
		query.removeEventListener("change", onChange);
	};
}

export function applyTheme(resolved: ResolvedTheme): void {
	document.documentElement.classList.toggle("dark", resolved === "dark");
}
