import { expect, test } from "bun:test";

import { renderTemplate } from "./message-template";

test("placeholders take the value of the log attribute they name", () => {
	expect(renderTemplate("GetCartAsync called with userId={userId}", { userId: "u-42" })).toBe(
		"GetCartAsync called with userId=u-42",
	);
	expect(renderTemplate("{a} then {a} and {b.c}", { a: "1", "b.c": "2" })).toBe("1 then 1 and 2");
});

test("placeholders without a matching attribute stay as written", () => {
	expect(renderTemplate("moved {count} items to {target}", { count: "3" })).toBe("moved 3 items to {target}");
	expect(renderTemplate("{constructor} {toString}", {})).toBe("{constructor} {toString}");
	expect(renderTemplate('{"user": "{userId}"}', { userId: "u-1" })).toBe('{"user": "u-1"}');
	expect(renderTemplate("no placeholders { here }", { here: "x" })).toBe("no placeholders { here }");
});
