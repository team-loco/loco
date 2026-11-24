import { expect, test } from "bun:test";
import { pickActive } from "./ContextProvider";

test("keeps an explicit scope URL when discovery lists only an unrelated membership", () => {
	expect(pickActive([{ id: "unrelated-organization" }], "direct-grant-organization", null)).toBe(
		"direct-grant-organization",
	);
	expect(pickActive([{ id: "unrelated-workspace" }], "direct-grant-workspace", null)).toBe("direct-grant-workspace");
});

test("keeps discovery fallback when there is no scope in the URL", () => {
	expect(pickActive([{ id: "first" }, { id: "second" }], undefined, null)).toBe("first");
	expect(pickActive([], undefined, null)).toBe(null);
});

test("uses a remembered id only when the loaded list contains it", () => {
	expect(pickActive([{ id: "first" }, { id: "remembered" }], undefined, "remembered")).toBe("remembered");
	expect(pickActive([{ id: "first" }], undefined, "remembered")).toBe("first");
	expect(pickActive([], undefined, "remembered")).toBe(null);
});

test("picks nothing from storage while the list is loading", () => {
	expect(pickActive(undefined, undefined, "remembered")).toBe(null);
	expect(pickActive(undefined, "from-url", "remembered")).toBe("from-url");
});
