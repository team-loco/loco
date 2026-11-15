import { describe, expect, test } from "bun:test";
import { createLoader, createSerializer } from "nuqs";
import { create } from "@bufbuild/protobuf";
import { LogEntrySchema } from "@gen/loco/observability/v1/observability_pb";
import { ResourceSchema } from "@gen/loco/resource/v1/resource_pb";

import {
	addRecent,
	agoShort,
	BUILD_ID_LABEL,
	buildBackendQuery,
	durationBounds,
	parseQuery,
	pinnedKey,
	QUERY_PARSERS,
	queryValues,
	recentKey,
	recentQuery,
	shownRecent,
	tokenSupport,
	toQuery,
	type RecentQuery,
	type Token,
} from "./query";
import { toRows } from "./rows";

const loadQuery = createLoader(QUERY_PARSERS);
const readQuery = (search: string) => toQuery(loadQuery(search));

describe("regional telemetry filters", () => {
	test("filters recorded regions without requiring current resources or backend discovery", () => {
		const { tokens, text } = parseQuery("@region:US-* @region:eu-west-1 -@region:us-west-* timeout");
		const query = buildBackendQuery(tokens, text, [], null);
		expect(query).toEqual({
			parsed: {
				search: "timeout",
				levels: [],
				excludedLevels: [],
				labels: [],
				excludedLabels: [],
				logAttributes: [],
				excludedLogAttributes: [],
				jsonFields: [],
				excludedJsonFields: [],
				traceIds: [],
				excludedTraceIds: [],
				regions: ["US-*", "eu-west-1"],
				excludedRegions: ["us-west-*"],
				excludedResourceIds: [],
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
		const { tokens } = parseQuery("@resource:web @level:error -@replica:web-1 @trace:abc -@trace:def");
		expect(tokenSupport("logs", tokens).map((s) => s.ok)).toEqual([true, true, true, true, false]);
		expect(tokenSupport("metrics", tokens).map((s) => s.ok)).toEqual([true, false, false, false, false]);
		expect(tokenSupport("traces", tokens).map((s) => s.ok)).toEqual([true, false, false, true, false]);
		expect(tokenSupport("traces", tokens)[4]?.reason).toBe("Excluding @trace isn't supported yet");
	});

	test("applies only the first filter of a single-value key", () => {
		const { tokens } = parseQuery("@trace:0123456789abcdef0123456789abcdef @trace:fedcba9876543210fedcba9876543210");
		expect(tokenSupport("traces", tokens).map((s) => s.ok)).toEqual([true, false]);
	});

	test("reads resource links and replica shortcuts from the URL", () => {
		expect(readQuery("?r=web,api&q=timeout").tokens.map((t) => t.value)).toEqual(["web", "api"]);
		expect(readQuery("?q=pod:web-7c41e9-x2kd9").tokens).toEqual([
			{ neg: false, key: "replica", value: "web-7c41e9-x2kd9" },
		]);
	});

	test("writes the query into q and drops the resource link", () => {
		const written = createSerializer(QUERY_PARSERS)(
			"?r=web&view=logs",
			queryValues([{ neg: true, key: "level", value: "debug" }], "slow"),
		);
		const params = new URLSearchParams(written);
		expect(params.get("r")).toBeNull();
		expect(params.get("q")).toBe("-@level:debug slow");
		expect(params.get("view")).toBe("logs");
	});
});

describe("build log filters", () => {
	test("reads the build deep link and the @build token", () => {
		const token: Token = { neg: false, key: "build", value: "b-123" };
		expect(readQuery("?q=build:b-123").tokens).toEqual([token]);
		expect(parseQuery("@build:b-123 failed")).toEqual({ tokens: [token], text: "failed" });
	});

	test("filters on the build ID label across environments", () => {
		const { tokens, text } = parseQuery("@resource:api @build:b-123");
		const raw = create(ResourceSchema, { id: "r1", name: "api" });
		const query = buildBackendQuery(tokens, text, [{ id: "r1", name: "api", color: "", regions: [], raw }], null);
		expect(query.parsed.labels).toEqual([{ key: BUILD_ID_LABEL, values: ["b-123"] }]);
		expect(query.resourceIds).toEqual(["r1"]);
		expect(query.allEnvironments).toBe(true);
	});

	test("applies build filters to logs only, any of the included ones and none of the excluded", () => {
		const { tokens } = parseQuery("@build:a @build:b -@build:c");
		expect(tokenSupport("logs", tokens).map((s) => s.ok)).toEqual([true, true, true]);
		expect(tokenSupport("traces", tokens).map((s) => s.ok)).toEqual([false, false, false]);
		const query = buildBackendQuery(tokens, "", [], null);
		expect(query.parsed.labels).toEqual([{ key: BUILD_ID_LABEL, values: ["a", "b"] }]);
		expect(query.parsed.excludedLabels).toEqual([{ key: BUILD_ID_LABEL, values: ["c"] }]);
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

	test("keeps eight and shows five, leaving out the current and pinned queries", () => {
		let list: RecentQuery[] = [];
		for (let i = 0; i < 10; i++) list = addRecent(list, `q${String(i)}`, i);
		expect(list.map((r) => r.q)).toEqual(["q9", "q8", "q7", "q6", "q5", "q4", "q3", "q2"]);
		expect(shownRecent(list, "q8", []).map((r) => r.q)).toEqual(["q9", "q7", "q6", "q5", "q4"]);
		expect(shownRecent(list, "q8", ["q9", "q6"]).map((r) => r.q)).toEqual(["q7", "q5", "q4", "q3", "q2"]);
	});

	test("keeps pinned searches per organization, workspace and view", () => {
		const keys = [
			pinnedKey("o1", "w1", "logs"),
			pinnedKey("o1", "w1", "events"),
			pinnedKey("o1", "w2", "logs"),
			pinnedKey("o2", "w1", "logs"),
		];
		expect(new Set(keys).size).toBe(keys.length);
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

describe("trace duration filters", () => {
	test("reads milliseconds by default and other units when given", () => {
		expect(durationBounds(parseQuery("@duration:>500").tokens)).toEqual({ minNanos: 500_000_000n });
		expect(durationBounds(parseQuery("@duration:<1.5s").tokens)).toEqual({ maxNanos: 1_500_000_000n });
		expect(durationBounds(parseQuery("@duration:>=250us").tokens)).toEqual({ minNanos: 250_000n });
	});
	test("keeps the tightest bound on each side", () => {
		const { tokens } = parseQuery("@duration:>100 @duration:>200 @duration:<900 @duration:<2s");
		expect(durationBounds(tokens)).toEqual({ minNanos: 200_000_000n, maxNanos: 900_000_000n });
	});
	test("rejects a value without a comparison", () => {
		expect(durationBounds(parseQuery("@duration:500").tokens)).toBeNull();
	});
	test("applies only to traces", () => {
		expect(tokenSupport("logs", parseQuery("@duration:>500").tokens)[0]?.ok).toBe(false);
	});
});

describe("log attribute, trace and exclusion filters", () => {
	const raw = create(ResourceSchema, { id: "r1", name: "api" });
	const api = { id: "r1", name: "api", color: "", regions: [], raw };

	test("excludes levels, resources, replicas and builds without narrowing the rest", () => {
		const { tokens } = parseQuery("-@level:debug -@resource:api -@replica:web-1 -@build:b-1");
		const query = buildBackendQuery(tokens, "", [api], null);
		expect(query.parsed.levels).toEqual([]);
		expect(query.parsed.excludedLevels).toContain("DEBUG");
		expect(query.parsed.excludedResourceIds).toEqual(["r1"]);
		expect(query.resourceIds).toEqual([]);
		expect(query.parsed.excludedLabels).toEqual([
			{ key: "k8s.pod.name", values: ["web-1"] },
			{ key: BUILD_ID_LABEL, values: ["b-1"] },
		]);
		expect(query.allEnvironments).toBe(false);
		expect(query.impossible).toBe(false);
	});

	test("filters one trace and skips a malformed trace ID", () => {
		const id = "0123456789ABCDEF0123456789ABCDEF";
		expect(buildBackendQuery(parseQuery(`@trace:${id}`).tokens, "", [], null).parsed.traceIds).toEqual([
			id.toLowerCase(),
		]);
		expect(buildBackendQuery(parseQuery("@trace:abc").tokens, "", [], null).impossible).toBe(true);
		const excluded = buildBackendQuery(parseQuery("-@trace:abc").tokens, "", [], null);
		expect(excluded.parsed.excludedTraceIds).toEqual([]);
		expect(excluded.impossible).toBe(false);
	});

	test("reads @method, @path and @attr as log attributes and @label as resource attributes", () => {
		const { tokens } = parseQuery(
			"@method:GET -@path:/healthz @attr:http.response.status_code=500 -@attr:user=bot @label:team=a",
		);
		const query = buildBackendQuery(tokens, "", [], null);
		expect(query.parsed.logAttributes).toEqual([
			{ key: "method", values: ["GET"] },
			{ key: "http.response.status_code", values: ["500"] },
		]);
		expect(query.parsed.excludedLogAttributes).toEqual([
			{ key: "path", values: ["/healthz"] },
			{ key: "user", values: ["bot"] },
		]);
		expect(query.parsed.labels).toEqual([{ key: "team", values: ["a"] }]);
	});

	test("matches any of the values given for one key and none of the excluded ones", () => {
		const { tokens } = parseQuery(
			"@method:GET @method:POST -@replica:a -@replica:b -@path:/a -@path:/b @json:user.id=42 -@json:level=debug -@json:level=trace",
		);
		expect(tokenSupport("logs", tokens).every((s) => s.ok)).toBe(true);
		const { parsed } = buildBackendQuery(tokens, "", [], null);
		expect(parsed.logAttributes).toEqual([{ key: "method", values: ["GET", "POST"] }]);
		expect(parsed.excludedLabels).toEqual([{ key: "k8s.pod.name", values: ["a", "b"] }]);
		expect(parsed.excludedLogAttributes).toEqual([{ key: "path", values: ["/a", "/b"] }]);
		expect(parsed.jsonFields).toEqual([{ key: "user.id", values: ["42"] }]);
		expect(parsed.excludedJsonFields).toEqual([{ key: "level", values: ["debug", "trace"] }]);
	});

	test("rejects malformed pairs and reserved labels", () => {
		const { tokens } = parseQuery("@attr:novalue @json:=1 @label:loco.workspace.id=w @label:loco.io/build-id=b");
		expect(tokenSupport("logs", tokens).map((s) => s.ok)).toEqual([false, false, false, true]);
	});

	test("quotes values with spaces or quotes in the URL", () => {
		const token: Token = { neg: true, key: "attr", value: 'user_agent=curl 8 "x"' };
		const written = createSerializer(QUERY_PARSERS)("", queryValues([token], "slow"));
		expect(readQuery(written).tokens).toEqual([token]);
		expect(readQuery(written).text).toBe("slow");
	});
});
