import { expect, test } from "bun:test";

import { charCount, clampChars } from "./characters";

test("an emoji counts as one character", () => {
	expect(charCount("a😀b")).toBe(3);
	expect(clampChars("😀😀😀", 2)).toBe("😀😀");
	expect(clampChars("abc", 5)).toBe("abc");
});
