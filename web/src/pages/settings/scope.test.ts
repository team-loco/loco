import { expect, test } from "bun:test";
import { createLoader } from "nuqs";

import { requestedScope, SETTINGS_PARAMS } from "./scope";

const loadScope = createLoader(SETTINGS_PARAMS);

test("requestedScope reads scope and maps the old tab values", () => {
	const scopeOf = (search: string) => requestedScope(loadScope(search));
	expect(scopeOf("?scope=org")).toBe("org");
	expect(scopeOf("?scope=account")).toBe("account");
	expect(scopeOf("?tab=ws")).toBe("workspace");
	expect(scopeOf("?tab=org")).toBe("org");
	expect(scopeOf("?scope=bogus&tab=org")).toBe("org");
	expect(scopeOf("")).toBe("workspace");
});
