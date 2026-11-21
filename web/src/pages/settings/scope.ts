import { parseAsString, parseAsStringLiteral } from "nuqs";

import { pageTokensParser } from "@/lib/paging";
import { SETTINGS_SCOPES } from "@/lib/routes";

// q, pages and size belong to the organization scope's workspace list; switching scope clears them.
export const SETTINGS_PARAMS = {
	scope: parseAsStringLiteral(SETTINGS_SCOPES),
	q: parseAsString,
	pages: pageTokensParser,
	size: parseAsString,
};
