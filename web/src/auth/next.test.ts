import { describe, expect, test } from "bun:test";

import { nextPathFrom, nextPathState, safeNextPath } from "./next";

describe("safeNextPath", () => {
	test("accepts app paths with a query", () => {
		expect(safeNextPath("/org/o/wks/w/nope?env=production")).toBe("/org/o/wks/w/nope?env=production");
	});

	test.each([
		["protocol-relative", "//evil.example/x"],
		["backslash", "/\\evil.example/x"],
		["inner backslash", "/a\\b"],
		["scheme", "https://evil.example/x"],
		["javascript", "javascript:alert(1)"],
		["relative", "dashboard"],
		["empty", ""],
		["non-string", 42],
		["null", null],
	])("rejects %s", (_, path) => {
		expect(safeNextPath(path)).toBeNull();
	});
});

describe("nextPathFrom", () => {
	test("reads the path ProtectedLayout puts in router state", () => {
		expect(nextPathFrom(nextPathState("/org/o/wks/w/nope"))).toBe("/org/o/wks/w/nope");
	});

	test("ignores missing or unsafe state", () => {
		expect(nextPathFrom(null)).toBeNull();
		expect(nextPathFrom({ signInError: "x" })).toBeNull();
		expect(nextPathFrom(nextPathState("//evil.example"))).toBeNull();
	});
});
