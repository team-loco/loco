import { createCn } from "cn/config";

const THEME_RADII = ["control", "control-inner", "card", "overlay"];

export const cn = createCn({ extend: { theme: { radius: THEME_RADII } } });

export function formatShortId(id: string): string {
	if (!id) return "";
	return id.slice(-8);
}
