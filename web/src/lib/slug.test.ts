import { expect, test } from "bun:test";

import { slug } from "./slug";

test("slug matches the API's slug of a name", () => {
	expect(slug("My App")).toBe("my-app");
	expect(slug("__API v2!!")).toBe("api-v2");
	expect(slug("123")).toBe("123");
	expect(slug("!!!")).toBe("");
	expect(slug("A".repeat(70))).toBe("a".repeat(63));
	expect(slug(`${"a".repeat(62)} b`)).toBe("a".repeat(62));
});
