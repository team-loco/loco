import { expect, test } from "bun:test";

import { SLUG_DIGIT_LEAD, SLUG_RULE, nameError, slug, slugError } from "./slug";

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

test("slugError follows the API's slug rules", () => {
	const slugErr = (value: string, taken = new Set<string>()) => slugError(value, taken);
	expect(slugErr("")).toBe("Enter a slug");
	for (const bad of ["!!!", "QA Echo", "Web", "🚀rocket", "<script>x</script>", "-bad-", " web", "x".repeat(64)])
		expect(slugErr(bad)).toBe(SLUG_RULE);
	for (const digitLed of ["123cart", "7", "1-web"]) expect(slugErr(digitLed)).toBe(SLUG_DIGIT_LEAD);
	for (const ok of ["web", "qa-echo", "x".repeat(63)]) expect(slugErr(ok)).toBeNull();
	expect(slugErr("localhost")).toBe("localhost is reserved");
	expect(slugErr("web", new Set(["web"]))).toBe("web already exists in this workspace");
});
