import { expect, test } from "bun:test";

import { NEW_SERVICE_PARAM, NEW_SERVICE_VALUE, newServicePath } from "./routes";

test("newServicePath opens the new service dialog on the dashboard and keeps the environment", () => {
	const url = new URL(newServicePath("o1", "w1", { env: "staging" }), "https://loco.test");
	expect(url.pathname).toBe("/org/o1/wks/w1");
	expect(url.searchParams.get("env")).toBe("staging");
	expect(url.searchParams.get(NEW_SERVICE_PARAM)).toBe(NEW_SERVICE_VALUE);
});
