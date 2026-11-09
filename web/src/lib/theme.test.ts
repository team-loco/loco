import { describe, expect, test } from "bun:test";

import { parseMode, resolveMode, toggledMode } from "./theme";

describe("theme mode", () => {
	test("reads a stored explicit mode", () => {
		expect(parseMode("light")).toBe("light");
		expect(parseMode("dark")).toBe("dark");
	});

	test("follows the system without a valid stored mode", () => {
		expect(parseMode(null)).toBe("system");
		expect(parseMode("sepia")).toBe("system");
	});

	test("system mode resolves to the system preference", () => {
		expect(resolveMode("system", true)).toBe("dark");
		expect(resolveMode("system", false)).toBe("light");
	});

	test("an explicit mode ignores the system preference", () => {
		expect(resolveMode("light", true)).toBe("light");
		expect(resolveMode("dark", false)).toBe("dark");
	});

	test("toggling away from the system preference stores the opposite theme", () => {
		expect(toggledMode("system", false)).toBe("dark");
		expect(toggledMode("system", true)).toBe("light");
	});

	test("toggling back to the system preference follows the system again", () => {
		expect(toggledMode("dark", false)).toBe("system");
		expect(toggledMode("light", true)).toBe("system");
	});

	test("toggling an explicit theme that matches the system stores the opposite", () => {
		expect(toggledMode("dark", true)).toBe("light");
		expect(toggledMode("light", false)).toBe("dark");
	});
});
