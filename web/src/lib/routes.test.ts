import { expect, test } from "bun:test";

import { inResourcesSection, newResourcePath } from "./routes";

test("newResourcePath opens the new resource flow on the resources page and keeps other params", () => {
	const url = new URL(newResourcePath("o1", "w1", { env: "staging", new: "service" }), "https://loco.test");
	expect(url.pathname).toBe("/org/o1/wks/w1/resources");
	expect(url.searchParams.get("env")).toBe("staging");
	expect(url.searchParams.getAll("new")).toEqual(["1"]);
	expect(newResourcePath("o1", "w1")).toBe("/org/o1/wks/w1/resources?new=1");
});

test("inResourcesSection covers the resources list and resource pages of that workspace", () => {
	expect(inResourcesSection("/org/o1/wks/w1/resources", "o1", "w1")).toBe(true);
	expect(inResourcesSection("/org/o1/wks/w1/resource/r1", "o1", "w1")).toBe(true);
	expect(inResourcesSection("/org/o1/wks/w1", "o1", "w1")).toBe(false);
	expect(inResourcesSection("/org/o1/wks/w1/settings", "o1", "w1")).toBe(false);
	expect(inResourcesSection("/org/o1/wks/w2/resources", "o1", "w1")).toBe(false);
});
