import { parseAsString, parseAsStringLiteral } from "nuqs";

import { SETTINGS_SCOPES, type SettingsScope } from "@/lib/routes";

export const SETTINGS_PARAMS = { scope: parseAsStringLiteral(SETTINGS_SCOPES), tab: parseAsString };

const LEGACY_TABS: ReadonlyMap<string, SettingsScope> = new Map([
	["ws", "workspace"],
	["org", "org"],
]);

export function requestedScope({ scope, tab }: { scope: SettingsScope | null; tab: string | null }): SettingsScope {
	return scope ?? LEGACY_TABS.get(tab ?? "") ?? "workspace";
}
