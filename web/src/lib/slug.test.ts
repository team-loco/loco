import { expect, test } from "bun:test";

import { derivedSlugError, nameError, slug } from "./slug";

test("slug matches the API's slug of a name", () => {
	expect(slug("My App")).toBe("my-app");
	expect(slug("__API v2!!")).toBe("api-v2");
	expect(slug("123")).toBe("123");
	expect(slug("!!!")).toBe("");
	expect(slug("Café Orders")).toBe("cafe-orders");
	expect(slug("Zürich")).toBe("zurich");
	expect(slug("Crème Brûlée ☕")).toBe("creme-brulee");
	expect(slug("A".repeat(70))).toBe("a".repeat(63));
	expect(slug(`${"a".repeat(62)} b`)).toBe("a".repeat(62));
});

test("nameError follows the API's name rules", () => {
	const nameErr = (name: string, taken = new Set<string>()) => nameError(name, taken);
	for (const blank of ["", "   "]) expect(nameErr(blank)).toBe("Enter a name");
	for (const ok of ["QA Echo", "🚀rocket", " web ", "🚀".repeat(63)]) expect(nameErr(ok)).toBeNull();
	expect(nameErr("x".repeat(64))).toBe("Use at most 63 characters");
	expect(nameErr(" QA Echo ", new Set(["QA Echo"]))).toBe("QA Echo already exists in this workspace");
});

test("derivedSlugError rejects names whose slug the API refuses", () => {
	const err = (name: string, taken = new Set<string>()) => derivedSlugError(name, taken);
	for (const ok of ["QA Echo", "🚀 Rocket", "Café Orders", "x".repeat(63)]) expect(err(ok)).toBeNull();
	for (const noLetter of ["🚀", "!!!", "日本"]) expect(err(noLetter)).toBe("Include a letter from a to z");
	for (const digitLed of ["3D Viewer", "7", "1-web"]) expect(err(digitLed)).toBe("Start the name with a letter");
	expect(err(" Localhost ")).toBe("Localhost is reserved");
	expect(err("My-App", new Set(["my-app"]))).toBe("Too close to an existing name in this workspace");
});
