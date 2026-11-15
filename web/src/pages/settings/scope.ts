import { parseAsString, parseAsStringLiteral } from "nuqs";

import { SETTINGS_SCOPES } from "@/lib/routes";

// q, page and size belong to the organization scope's workspace list; switching scope clears them.
export const SETTINGS_PARAMS = {
	scope: parseAsStringLiteral(SETTINGS_SCOPES).withDefault("workspace"),
	q: parseAsString,
	page: parseAsString,
	size: parseAsString,
};
