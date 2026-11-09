import { describe, expect, test } from "bun:test";
import { create } from "@bufbuild/protobuf";
import { LogEntrySchema } from "@gen/loco/observability/v1/observability_pb";

import { buildBackendQuery, parseQuery, readQuery, tokenSupport, writeQuery } from "./query";
import { toRows } from "./rows";

describe("regional telemetry filters", () => {
	test("filters recorded regions without requiring current resources or backend discovery", () => {
		const { tokens, text } = parseQuery("@region:US-* @region:eu-west-1 -@region:us-west-* timeout");
		const query = buildBackendQuery(tokens, text, []);
		expect(query).toEqual({
			parsed: {
				search: "timeout",
				levels: [],
				labels: {},
				regions: ["US-*", "eu-west-1"],
				excludedRegions: ["us-west-*"],
			},
			resourceIds: [],
			impossible: false,
		});
	});
	test("keeps unfiltered workspace history available without active resources", () => {
		expect(buildBackendQuery([], "", []).impossible).toBe(false);
		const { tokens } = parseQuery("@resource:missing");
		expect(buildBackendQuery(tokens, "", []).impossible).toBe(true);
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
		const query = buildBackendQuery(tokens, text, []);
		expect(query.parsed.levels).toContain(entry.severity);
		expect(query.parsed.search).toBe(entry.body);
	});

	test("keeps debug lines when the level filter asks for them", () => {
		const { tokens } = parseQuery("@level:debug");
		expect(buildBackendQuery(tokens, "", []).parsed.levels).toContain("DEBUG");
	});

	test("keeps unclassified records out of other levels and excluded INFO", () => {
		for (const filter of ["@level:error", "@level:warn", "@level:debug", "-@level:info"]) {
			const { tokens, text } = parseQuery(filter);
			expect(buildBackendQuery(tokens, text, []).parsed.levels).not.toContain("");
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
