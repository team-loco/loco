import type { SettingsScope } from "@/lib/routes";

export const SCOPES: readonly SettingsScope[] = ["org", "workspace", "account"];

const LEGACY_TABS: ReadonlyMap<string, SettingsScope> = new Map([
	["ws", "workspace"],
	["org", "org"],
	["audit", "org"],
]);

export function scopeFromParams(params: URLSearchParams): SettingsScope {
	const scope = SCOPES.find((s) => s === params.get("scope"));
	return scope ?? LEGACY_TABS.get(params.get("tab") ?? "") ?? "workspace";
}
