import { describe, expect, test } from "bun:test";
import { Code, ConnectError } from "@connectrpc/connect";
import { createLoader, createSerializer } from "nuqs";
import { create, toBinary } from "@bufbuild/protobuf";
import { BadRequestSchema, ErrorInfoSchema } from "@gen/google/rpc/error_details_pb";
import { LogEntrySchema } from "@gen/loco/observability/v1/observability_pb";

import {
	addRecent,
	agoShort,
	buildBackendQuery,
	durationBounds,
	parseQuery,
	pinnedKey,
	queryError,
	queryKeyReason,
	queryStr,
	searchWords,
	quoteValue,
	QUERY_PARSERS,
	queryValues,
	recentKey,
	recentQuery,
	shownRecent,
	tokenSupport,
	toQuery,
	unquoteValue,
	type RecentQuery,
	type Token,
} from "./query";
import { toRows } from "./rows";

const loadQuery = createLoader(QUERY_PARSERS);
const readQuery = (search: string) => toQuery(loadQuery(search));

describe("regional telemetry filters", () => {
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
	test("labels records without a severity INFO", () => {
		const row = toRows([create(LogEntrySchema, { body: "unstructured stdout", severity: "" })], new Map())[0];
		expect(row?.label).toBe("INFO");
	});
});

describe("omnibar tokens per view", () => {
	test("marks keys a view can't filter on and keeps the ones it can", () => {
		const { tokens } = parseQuery("@resource:web @level:error -@replica:web-1 @trace:abc -@trace:def");
		expect(tokenSupport("logs", tokens).map((s) => s.ok)).toEqual([true, true, true, true, true]);
		expect(tokenSupport("metrics", tokens).map((s) => s.ok)).toEqual([true, false, false, false, false]);
		expect(tokenSupport("traces", tokens).map((s) => s.ok)).toEqual([true, false, false, true, false]);
		expect(tokenSupport("traces", tokens)[4]?.reason).toBe("Excluding @trace isn't supported yet");
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
	test("applies to traces and is an attribute in logs", () => {
		const { tokens } = parseQuery("@duration:>500");
		expect(tokenSupport("metrics", tokens)[0]?.ok).toBe(false);
		expect(tokenSupport("logs", tokens)[0]?.ok).toBe(true);
	});
});

describe("log queries", () => {
	test("makes any field a chip and sends the bar's text as the query", () => {
		const { tokens, text } = parseQuery(
			'@region:US-* -@level:debug @http.status_code:>=500 @roles[0]:admin @path:"/a:b" timeout',
		);
		expect(tokens.map((t) => `${t.neg ? "-" : ""}${t.key}=${t.value}`)).toEqual([
			"region=US-*",
			"-level=debug",
			"http.status_code=>=500",
			"roles[0]=admin",
			'path="/a:b"',
		]);
		expect(buildBackendQuery(tokens, text, null)).toEqual({
			query: '@region:US-* -@level:debug @http.status_code:>=500 @roles[0]:admin @path:"/a:b" timeout',
			resourceIds: [],
			allEnvironments: false,
			impossible: false,
		});
	});

	test("keeps a query with AND, OR or parentheses whole, and groups it when a chip joins", () => {
		const q = '(@level:error OR @status:>=500) -@path:/healthz "x OR y"';
		expect(parseQuery(q)).toEqual({ tokens: [], text: q });
		expect(parseQuery('@a:b "x OR (y)"').tokens).toHaveLength(1);
		const token: Token = { neg: false, key: "trace", value: "abc" };
		expect(queryStr([token], "@level:error OR @level:warn")).toBe("@trace:abc (@level:error OR @level:warn)");
		expect(queryStr([token], "timeout")).toBe("@trace:abc timeout");
	});

	test("quotes values that would read as syntax, and round trips them through the URL", () => {
		expect(quoteValue("GET")).toBe("GET");
		for (const value of ['curl 8 "x"', "/a:b", "/api/*", ">5", "1..2", "(x)", ""]) {
			expect(unquoteValue(quoteValue(value))).toBe(value);
			expect(quoteValue(value).startsWith('"')).toBe(true);
		}
		const token: Token = { neg: true, key: "user_agent", value: quoteValue('curl 8 "x"') };
		const written = createSerializer(QUERY_PARSERS)("", queryValues([token], "slow"));
		expect(readQuery(written)).toEqual({ tokens: [token], text: "slow" });
	});

	test("says why an attribute key can't be filtered", () => {
		expect(queryKeyReason("http.status_code")).toBeNull();
		expect(queryKeyReason("loco.io/build-id")).toBeNull();
		expect(queryKeyReason("loco.workspace.id")).toContain("Loco sets");
		expect(queryKeyReason("level")).toContain("built-in");
		expect(queryKeyReason("has space")).toContain("can't be written");
	});

	test("highlights the free text a query searches for", () => {
		expect(searchWords('timeout (@level:error OR "payment failed") -retry -@path:"a b" AND x')).toEqual([
			"timeout",
			"payment failed",
			"x",
		]);
	});

	test("reads the API's parse error and its position", () => {
		const error = new ConnectError("quote values that contain : at position 7", Code.InvalidArgument);
		const violations = create(BadRequestSchema, {
			fieldViolations: [{ field: "query", description: "quote values that contain :" }],
		});
		const info = create(ErrorInfoSchema, { reason: "LOG_QUERY_SYNTAX", metadata: { position: "7" } });
		error.details.push(
			{ type: BadRequestSchema.typeName, value: toBinary(BadRequestSchema, violations) },
			{ type: ErrorInfoSchema.typeName, value: toBinary(ErrorInfoSchema, info) },
		);
		expect(queryError(error, "@path:/a:b")).toEqual({
			query: "@path:/a:b",
			message: "quote values that contain :",
			position: 7,
		});
		expect(queryError(new ConnectError("bad at position 3", Code.InvalidArgument), "x")).toBeNull();
		expect(queryError(new ConnectError("boom", Code.Internal), "x")).toBeNull();
	});

	test("reads build logs across environments", () => {
		expect(buildBackendQuery(parseQuery("@build:b-123").tokens, "", null).allEnvironments).toBe(true);
		expect(buildBackendQuery(parseQuery("-@build:b-123").tokens, "", null).allEnvironments).toBe(false);
	});

	test("scopes to the readable resources and waits until one is known", () => {
		expect(buildBackendQuery([], "", ["r1"]).resourceIds).toEqual(["r1"]);
		expect(buildBackendQuery([], "", []).impossible).toBe(true);
	});
});
