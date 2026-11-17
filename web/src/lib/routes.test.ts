import { expect, test } from "bun:test";

import { createLoader } from "nuqs";

import { QUERY_PARSERS, toQuery } from "@/pages/observability/query";

import { toWindow, WINDOW_PARSERS } from "./obs-time";
import { DAY_MS } from "./time";
import { buildLogsPath, draftSearch, inResourcesSection, newResourcePath, resourcePath, withEnv } from "./routes";

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
	expect(url.searchParams.get("q")).toBe("@build:b-123");
	expect(toQuery(createLoader(QUERY_PARSERS)(url)).tokens).toEqual([{ neg: false, key: "build", value: "b-123" }]);
	expect(toWindow(createLoader(WINDOW_PARSERS)(url), DAY_MS)).toEqual({ kind: "relative", spanMs: DAY_MS });
});

test("resourcePath and withEnv carry the environment and keep the path's other params", () => {
	expect(resourcePath("o1", "w1", "r1")).toBe("/org/o1/wks/w1/resource/r1");
	expect(resourcePath("o1", "w1", "r1", { tab: "settings", env: "staging" })).toBe(
		"/org/o1/wks/w1/resource/r1?tab=settings&env=staging",
	);
	expect(withEnv("/org/o1/wks/w1/resources?view=map", "prod east")).toBe(
		"/org/o1/wks/w1/resources?view=map&env=prod+east",
	);
	expect(withEnv("/org/o1/wks/w1?env=staging", null)).toBe("/org/o1/wks/w1");
});
