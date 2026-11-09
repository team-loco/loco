import { expect, test } from "bun:test";

import { scopeFromParams } from "./scope";

test("scopeFromParams reads scope and maps the old tab values", () => {
	expect(scopeFromParams(new URLSearchParams("scope=org"))).toBe("org");
	expect(scopeFromParams(new URLSearchParams("scope=account"))).toBe("account");
	expect(scopeFromParams(new URLSearchParams("tab=ws"))).toBe("workspace");
	expect(scopeFromParams(new URLSearchParams("tab=org"))).toBe("org");
	expect(scopeFromParams(new URLSearchParams("tab=audit"))).toBe("org");
	expect(scopeFromParams(new URLSearchParams("scope=bogus&tab=org"))).toBe("org");
	expect(scopeFromParams(new URLSearchParams(""))).toBe("workspace");
});
