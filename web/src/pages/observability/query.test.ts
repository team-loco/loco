import { describe, expect, test } from "bun:test";
import { createLoader, createSerializer } from "nuqs";
import { create } from "@bufbuild/protobuf";
import { LogEntrySchema } from "@gen/loco/observability/v1/observability_pb";

import {
	addRecent,
	agoShort,
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
	test("applies only to traces", () => {
		expect(tokenSupport("logs", parseQuery("@duration:>500").tokens)[0]?.ok).toBe(false);
	});
});

describe("log attribute, trace and exclusion filters", () => {
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

describe("log queries", () => {
	test("sends the bar's filters as query text, writing attribute pairs as fields", () => {
		const { tokens, text } = parseQuery(
			"@region:US-* -@level:debug @attr:http.status_code=500 -@label:team=a @json:user.id=42 @method:GET timeout",
		);
		expect(buildBackendQuery(tokens, text, null)).toEqual({
			query: "@region:US-* -@level:debug @http.status_code:500 -@team:a @user.id:42 @method:GET timeout",
			resourceIds: [],
			allEnvironments: false,
			impossible: false,
		});
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
