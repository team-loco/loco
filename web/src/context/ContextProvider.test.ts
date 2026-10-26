import { expect, test } from "bun:test";
import { pickActive } from "./ContextProvider";

test("keeps an explicit scope URL when discovery lists only an unrelated membership", () => {
	expect(pickActive([{ id: "unrelated-organization" }], "direct-grant-organization", "test:org")).toBe(
		"direct-grant-organization",
	);
	expect(pickActive([{ id: "unrelated-workspace" }], "direct-grant-workspace", "test:workspace")).toBe(
		"direct-grant-workspace",
	);
});

test("keeps discovery fallback when there is no scope in the URL", () => {
	expect(pickActive([{ id: "first" }, { id: "second" }], undefined, "test:missing")).toBe("first");
	expect(pickActive([], undefined, "test:missing")).toBe(null);
});
