import { parseAsStringLiteral } from "nuqs";

import { SETTINGS_SCOPES } from "@/lib/routes";

export const SETTINGS_PARAMS = { scope: parseAsStringLiteral(SETTINGS_SCOPES).withDefault("workspace") };
