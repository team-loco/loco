import { describe, expect, test } from "bun:test";
import { create } from "@bufbuild/protobuf";
import { LogEntrySchema } from "@gen/loco/observability/v1/observability_pb";
import { ResourceSchema } from "@gen/loco/resource/v1/resource_pb";

import {
	addRecent,
	agoShort,
	BUILD_ID_LABEL,
	buildBackendQuery,
	parseQuery,
	readQuery,
	recentKey,
	recentQuery,
	shownRecent,
	tokenSupport,
	writeQuery,
	type RecentQuery,
	type Token,
} from "./query";
import { toRows } from "./rows";

describe("regional telemetry filters", () => {
	test("filters recorded regions without requiring current resources or backend discovery", () => {
		const { tokens, text } = parseQuery("@region:US-* @region:eu-west-1 -@region:us-west-* timeout");
		const query = buildBackendQuery(tokens, text, [], null);
		expect(query).toEqual({
			parsed: {
				search: "timeout",
				levels: [],
				labels: {},
				regions: ["US-*", "eu-west-1"],
				excludedRegions: ["us-west-*"],
			},
			resourceIds: [],
			allEnvironments: false,
			impossible: false,
		});
	});
	test("keeps unfiltered workspace history available without active resources", () => {
		expect(buildBackendQuery([], "", [], null).impossible).toBe(false);
		const { tokens } = parseQuery("@resource:missing");
		expect(buildBackendQuery(tokens, "", [], null).impossible).toBe(true);
	});
	test("attributes each row to the recorded region and preserves unknown resources", () => {
		const entries = ["us-east-1", "eu-west-1", ""].map((region) =>
			create(LogEntrySchema, {
				resourceId: "removed-resource",
				body: "historical record",
				resourceAttributes: region === "" ? {} : { "loco.region": region },
			}),
		);
		const rows = toRows(entries, new Map());
		expect(rows.map((row) => row.region)).toEqual(["us-east-1", "eu-west-1", ""]);
		expect(rows.map((row) => row.resourceName)).toEqual(["removed-", "removed-", "removed-"]);
		expect(new Set(rows.map((row) => row.key)).size).toBe(3);
	});
});

describe("log severity filters", () => {
	test("includes records displayed as INFO when they have no severity", () => {
		const entry = create(LogEntrySchema, { body: "unstructured stdout", severity: "" });
		const row = toRows([entry], new Map())[0];
		expect(row?.label).toBe("INFO");
		const { tokens, text } = parseQuery("@level:info unstructured stdout");
		const query = buildBackendQuery(tokens, text, [], null);
		expect(query.parsed.levels).toContain(entry.severity);
		expect(query.parsed.search).toBe(entry.body);
	});

	test("keeps debug lines when the level filter asks for them", () => {
		const { tokens } = parseQuery("@level:debug");
		expect(buildBackendQuery(tokens, "", [], null).parsed.levels).toContain("DEBUG");
	});

	test("keeps unclassified records out of other levels and excluded INFO", () => {
		for (const filter of ["@level:error", "@level:warn", "@level:debug", "-@level:info"]) {
			const { tokens, text } = parseQuery(filter);
			expect(buildBackendQuery(tokens, text, [], null).parsed.levels).not.toContain("");
		}
	});
});

describe("omnibar tokens per view", () => {
	test("marks keys a view can't filter on and keeps the ones it can", () => {
		const { tokens } = parseQuery("@resource:web @level:error -@replica:web-1 @trace:abc");
		expect(tokenSupport("logs", tokens).map((s) => s.ok)).toEqual([true, true, false, false]);
		expect(tokenSupport("metrics", tokens).map((s) => s.ok)).toEqual([true, false, false, false]);
		expect(tokenSupport("traces", tokens).map((s) => s.ok)).toEqual([true, false, false, true]);
		expect(tokenSupport("logs", tokens)[3]?.reason).toContain("isn't supported yet");
	});

	test("applies only the first filter of a single-value key", () => {
		const { tokens } = parseQuery("@replica:web-1 @replica:web-2");
		expect(tokenSupport("logs", tokens).map((s) => s.ok)).toEqual([true, false]);
	});

	test("reads resource links and replica shortcuts from the URL", () => {
		expect(readQuery(new URLSearchParams("r=web,api&q=timeout")).tokens.map((t) => t.value)).toEqual(["web", "api"]);
		expect(readQuery(new URLSearchParams("q=pod:web-7c41e9-x2kd9")).tokens).toEqual([
			{ neg: false, key: "replica", value: "web-7c41e9-x2kd9" },
		]);
	});

	test("writes the query into q and drops the resource link", () => {
		const params = writeQuery(
			new URLSearchParams("r=web&view=logs"),
			[{ neg: true, key: "level", value: "debug" }],
			"slow",
		);
		expect(params.get("r")).toBeNull();
		expect(params.get("q")).toBe("-@level:debug slow");
		expect(params.get("view")).toBe("logs");
	});
});

describe("build log filters", () => {
	test("reads the build deep link and the @build token", () => {
		const token: Token = { neg: false, key: "build", value: "b-123" };
		expect(readQuery(new URLSearchParams("q=build:b-123")).tokens).toEqual([token]);
		expect(parseQuery("@build:b-123 failed")).toEqual({ tokens: [token], text: "failed" });
	});

	test("filters on the build ID label across environments", () => {
		const { tokens, text } = parseQuery("@resource:api @build:b-123");
		const raw = create(ResourceSchema, { id: "r1", name: "api" });
		const query = buildBackendQuery(tokens, text, [{ id: "r1", name: "api", color: "", regions: [], raw }], null);
		expect(query.parsed.labels).toEqual({ [BUILD_ID_LABEL]: "b-123" });
		expect(query.resourceIds).toEqual(["r1"]);
		expect(query.allEnvironments).toBe(true);
	});

	test("applies one positive build filter to logs only", () => {
		const { tokens } = parseQuery("@build:a @build:b -@build:c");
		expect(tokenSupport("logs", tokens).map((s) => s.ok)).toEqual([true, false, false]);
		expect(tokenSupport("traces", tokens).map((s) => s.ok)).toEqual([false, false, false]);
		expect(buildBackendQuery(tokens, "", [], null).parsed.labels).toEqual({ [BUILD_ID_LABEL]: "a" });
	});
});

describe("recent queries", () => {
	const MIN = 60_000;
	const NOW = 1_800_000_000_000;

	test("keeps one list per organization, workspace and view", () => {
		const keys = [
			recentKey("o1", "w1", "logs"),
			recentKey("o1", "w1", "metrics"),
			recentKey("o1", "w1", "traces"),
			recentKey("o1", "w2", "logs"),
			recentKey("o2", "w1", "logs"),
		];
		expect(new Set(keys).size).toBe(keys.length);
		expect(recentKey("o1", "w1", "logs")).toBe(recentKey("o1", "w1", "logs"));
		expect(recentKey("o1", "w1", "events")).toBeNull();
	});

	test("puts the newest first and moves a repeated query to the top", () => {
		let list: RecentQuery[] = [];
		list = addRecent(list, "a", 1);
		list = addRecent(list, "b", 2);
		list = addRecent(list, "a", 3);
		expect(list).toEqual([
			{ q: "a", ts: 3 },
			{ q: "b", ts: 2 },
		]);
	});

	test("keeps eight and shows five, leaving out the current query", () => {
		let list: RecentQuery[] = [];
		for (let i = 0; i < 10; i++) list = addRecent(list, `q${String(i)}`, i);
		expect(list.map((r) => r.q)).toEqual(["q9", "q8", "q7", "q6", "q5", "q4", "q3", "q2"]);
		expect(shownRecent(list, "q8").map((r) => r.q)).toEqual(["q9", "q7", "q6", "q5", "q4"]);
	});

	test("saves only the parts of a query that apply to the view", () => {
		const { tokens, text } = parseQuery("@resource:api @level:error timeout");
		expect(recentQuery("logs", tokens, text)).toBe("@resource:api @level:error timeout");
		expect(recentQuery("metrics", tokens, text)).toBe("@resource:api");
		expect(recentQuery("traces", [], "")).toBe("");
	});

	test("labels the age in minutes, hours or days", () => {
		expect(agoShort(NOW - 20_000, NOW)).toBe("now");
		expect(agoShort(NOW - 5 * MIN, NOW)).toBe("5m");
		expect(agoShort(NOW - 3 * 60 * MIN, NOW)).toBe("3h");
		expect(agoShort(NOW - 2 * 1440 * MIN, NOW)).toBe("2d");
	});
});

describe("resource-scoped queries", () => {
	const raw = create(ResourceSchema, { id: "r1", name: "api" });
	const api = { id: "r1", name: "api", color: "", regions: [], raw };

	test("names the readable resources when the query names none", () => {
		const query = buildBackendQuery([], "", [api], ["r1"]);
		expect(query.resourceIds).toEqual(["r1"]);
		expect(query.impossible).toBe(false);
	});

	test("skips the query until a readable resource is known", () => {
		expect(buildBackendQuery([], "", [], []).impossible).toBe(true);
	});
});
