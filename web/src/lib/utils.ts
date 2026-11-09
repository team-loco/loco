import { createCn } from "cn/config";

const THEME_RADII = ["control", "control-inner", "card", "overlay"];

export const cn = createCn({ extend: { theme: { radius: THEME_RADII } } });

export function formatShortId(id: string): string {
	if (!id) return "";
	return id.slice(-8);
}

/** Returns `fallback` when `value` is null, undefined, or the empty string. */
export function nonEmpty(value: string | null | undefined, fallback: string): string {
	return value == null || value === "" ? fallback : value;
}
