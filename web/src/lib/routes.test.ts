import { expect, test } from "bun:test";

import { readQuery } from "@/pages/observability/query";

import { QUERY_MAX_MS, readWindow } from "./obs-time";
import { buildLogsPath, draftSearch, inResourcesSection, newResourcePath } from "./routes";

test("newResourcePath opens the new resource flow on the resources page and keeps other params", () => {
	const url = new URL(newResourcePath("o1", "w1", { env: "staging", new: "service" }), "https://loco.test");
	expect(url.pathname).toBe("/org/o1/wks/w1/resources");
	expect(url.searchParams.get("env")).toBe("staging");
	expect(url.searchParams.getAll("new")).toEqual(["1"]);
	expect(newResourcePath("o1", "w1")).toBe("/org/o1/wks/w1/resources?new=1");
});

test("draftSearch opens a draft in place of the new resource flow and keeps other params", () => {
	expect(draftSearch(new URLSearchParams("env=staging&new=1&q=web"), "d1")).toBe("?env=staging&q=web&draft=d1");
	expect(draftSearch(new URLSearchParams("draft=d1"), "d2")).toBe("?draft=d2");
});

test("inResourcesSection covers the resources list and resource pages of that workspace", () => {
	expect(inResourcesSection("/org/o1/wks/w1/resources", "o1", "w1")).toBe(true);
	expect(inResourcesSection("/org/o1/wks/w1/resource/r1", "o1", "w1")).toBe(true);
	expect(inResourcesSection("/org/o1/wks/w1", "o1", "w1")).toBe(false);
	expect(inResourcesSection("/org/o1/wks/w1/settings", "o1", "w1")).toBe(false);
	expect(inResourcesSection("/org/o1/wks/w2/resources", "o1", "w1")).toBe(false);
});

test("buildLogsPath opens a day of logs filtered to the build", () => {
	const url = new URL(buildLogsPath("o1", "w1", "b-123"), "https://loco.test");
	expect(url.pathname).toBe("/org/o1/wks/w1/observability");
	expect(url.searchParams.get("view")).toBe("logs");
	expect(readQuery(url.searchParams).tokens).toEqual([{ neg: false, key: "build", value: "b-123" }]);
	expect(readWindow(url.searchParams, QUERY_MAX_MS)).toEqual({ kind: "relative", spanMs: QUERY_MAX_MS });
});
