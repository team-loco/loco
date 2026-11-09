import { expect, test } from "bun:test";

import { newResourcePath } from "./routes";

test("newResourcePath opens the new resource page and keeps the environment", () => {
	const url = new URL(newResourcePath("o1", "w1", { env: "staging" }), "https://loco.test");
	expect(url.pathname).toBe("/org/o1/wks/w1/new");
	expect(url.searchParams.get("env")).toBe("staging");
	expect(newResourcePath("o1", "w1")).toBe("/org/o1/wks/w1/new");
});
