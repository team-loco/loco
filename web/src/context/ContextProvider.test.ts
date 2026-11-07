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

const storedKey = "test:stored";
const stored = new Map([[storedKey, "remembered"]]);
Object.defineProperty(globalThis, "window", {
	configurable: true,
	value: { localStorage: { getItem: (key: string) => stored.get(key) ?? null } },
});

test("uses a remembered id only when the loaded list contains it", () => {
	expect(pickActive([{ id: "first" }, { id: "remembered" }], undefined, storedKey)).toBe("remembered");
	expect(pickActive([{ id: "first" }], undefined, storedKey)).toBe("first");
	expect(pickActive([], undefined, storedKey)).toBe(null);
});

test("picks nothing from storage while the list is loading", () => {
	expect(pickActive(undefined, undefined, storedKey)).toBe(null);
	expect(pickActive(undefined, "from-url", storedKey)).toBe("from-url");
});
