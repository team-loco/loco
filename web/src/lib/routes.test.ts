import { expect, test } from "bun:test";

import { inResourcesSection, newResourcePath } from "./routes";

test("newResourcePath opens the new resource page and keeps the environment", () => {
	const url = new URL(newResourcePath("o1", "w1", { env: "staging" }), "https://loco.test");
	expect(url.pathname).toBe("/org/o1/wks/w1/new");
	expect(url.searchParams.get("env")).toBe("staging");
	expect(newResourcePath("o1", "w1")).toBe("/org/o1/wks/w1/new");
});

test("inResourcesSection covers the resources list, resource pages and the new resource page of that workspace", () => {
	expect(inResourcesSection("/org/o1/wks/w1/resources", "o1", "w1")).toBe(true);
	expect(inResourcesSection("/org/o1/wks/w1/resource/r1", "o1", "w1")).toBe(true);
	expect(inResourcesSection("/org/o1/wks/w1/new", "o1", "w1")).toBe(true);
	expect(inResourcesSection("/org/o1/wks/w1", "o1", "w1")).toBe(false);
	expect(inResourcesSection("/org/o1/wks/w1/settings", "o1", "w1")).toBe(false);
	expect(inResourcesSection("/org/o1/wks/w2/resources", "o1", "w1")).toBe(false);
});
