import { readStorage, removeStorage, writeStorage } from "./storage";

const modes = ["system", "light", "dark"] as const;
export type Mode = (typeof modes)[number];
export type ResolvedTheme = Exclude<Mode, "system">;

export const colorThemes = [
	{ id: "loco", name: "Loco" },
	{ id: "catppuccin", name: "Catppuccin" },
	{ id: "rose-pine", name: "Rosé Pine" },
	{ id: "gruvbox", name: "Gruvbox" },
	{ id: "tokyo-night", name: "Tokyo Night" },
	{ id: "paper", name: "Paper" },
] as const;
export type ColorTheme = (typeof colorThemes)[number]["id"];
export const defaultColorTheme: ColorTheme = "loco";

const modeKey = "loco:mode:v1";
const colorKey = "loco:color:v1";
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

export function parseColorTheme(value: string | null): ColorTheme {
	return colorThemes.find((theme) => theme.id === value)?.id ?? defaultColorTheme;
}

export function readColorTheme(): ColorTheme {
	return parseColorTheme(readStorage(colorKey));
}

export function storeColorTheme(color: ColorTheme): void {
	writeStorage(colorKey, color);
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

export function applyTheme(resolved: ResolvedTheme, color: ColorTheme): void {
	const root = document.documentElement;
	root.classList.toggle("dark", resolved === "dark");
	root.dataset["theme"] = color;
}
