import { describe, expect, test } from "bun:test";
import { create } from "@bufbuild/protobuf";
import { LogEntrySchema } from "@gen/loco/observability/v1/observability_pb";

import { buildBackendQuery, parseQuery } from "./query";
import { toRows } from "./rows";

describe("regional telemetry filters", () => {
	test("filters recorded regions without requiring current resources or backend discovery", () => {
		const { tokens, text } = parseQuery("@region:US-* @region:eu-west-1 -@region:us-west-* timeout");
		const query = buildBackendQuery(tokens, text, []);
		expect(query).toEqual({ parsed: { search: "timeout", levels: [], labels: {},
			regions: ["US-*", "eu-west-1"], excludedRegions: ["us-west-*"] }, resourceIds: [], impossible: false });
	});
	test("keeps unfiltered workspace history available without active resources", () => {
		expect(buildBackendQuery([], "", []).impossible).toBe(false);
		const { tokens } = parseQuery("@resource:missing");
		expect(buildBackendQuery(tokens, "", []).impossible).toBe(true);
	});
	test("attributes each row to the recorded region and preserves unknown resources", () => {
		const entries = ["us-east-1", "eu-west-1", ""].map((region) => create(LogEntrySchema, {
			resourceId: "removed-resource", body: "historical record", resourceAttributes: region === "" ? {} : { "loco.region": region },
		}));
		const rows = toRows(entries, new Map());
		expect(rows.map((row) => row.region)).toEqual(["us-east-1", "eu-west-1", ""]);
		expect(rows.map((row) => row.resourceName)).toEqual(["removed-", "removed-", "removed-"]);
		expect(new Set(rows.map((row) => row.key)).size).toBe(3);
	});
});
