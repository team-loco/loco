import { describe, expect, test } from "bun:test";

import { nextMode, pageTheme, parseColorTheme, parseMode, resolveMode } from "./theme";

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

	test("the toggle cycles light, dark and system", () => {
		expect(nextMode("light")).toBe("dark");
		expect(nextMode("dark")).toBe("system");
		expect(nextMode("system")).toBe("light");
	});
});

describe("page theme", () => {
	test("the splash is light with the Loco theme for a signed-out visitor", () => {
		expect(pageTheme("/", false, "dark", "gruvbox")).toEqual({ mode: "light", color: "loco" });
	});

	test("a signed-in visitor on the splash keeps their theme while redirected into the app", () => {
		expect(pageTheme("/", true, "dark", "gruvbox")).toEqual({ mode: "dark", color: "gruvbox" });
	});

	test("app pages use the stored mode and colour theme", () => {
		expect(pageTheme("/dashboard", false, "dark", "gruvbox")).toEqual({ mode: "dark", color: "gruvbox" });
		expect(pageTheme("/login", false, "dark", "rose-pine")).toEqual({ mode: "dark", color: "rose-pine" });
	});
});

describe("colour theme", () => {
	test("reads a stored colour theme", () => {
		expect(parseColorTheme("gruvbox")).toBe("gruvbox");
		expect(parseColorTheme("rose-pine")).toBe("rose-pine");
	});

	test("falls back to the Loco theme without a known stored theme", () => {
		expect(parseColorTheme(null)).toBe("loco");
		expect(parseColorTheme("github")).toBe("loco");
	});
});
