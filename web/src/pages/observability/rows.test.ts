import { describe, expect, test } from "bun:test";
import { create } from "@bufbuild/protobuf";
import { LogEntrySchema, type LogEntry } from "@gen/loco/observability/v1/observability_pb";

import { logJson, toRows, visibleLogRows } from "./rows";

const rows = toRows(
	[1, 2, 3].map((seconds) =>
		create(LogEntrySchema, {
			resourceId: "recommendation",
			timestamp: { seconds: BigInt(seconds) },
			body: `record ${String(seconds)}`,
		}),
	),
	new Map(),
);

describe("paused log history", () => {
	test("retains received live rows when disabling the stream clears its buffer", () => {
		const history = rows.slice(0, 1);
		const live = rows.slice(1, 2);
		const snapshot = visibleLogRows(history, live);
		expect(visibleLogRows(history, [], snapshot)).toEqual(snapshot);
		expect(visibleLogRows(history, [], snapshot)).toHaveLength(2);
	});
	test("keeps the paused snapshot stable when history refreshes", () => {
		const snapshot = visibleLogRows(rows.slice(0, 1), rows.slice(1, 2));
		expect(visibleLogRows(rows, [], snapshot)).toEqual(snapshot);
	});
	test("merges refreshed history and a new stream without repeating the overlap", () => {
		const history = rows.slice(0, 2);
		const live = rows.slice(1);
		expect(visibleLogRows(history, live).map((row) => row.entry.body)).toEqual(["record 3", "record 2", "record 1"]);
		expect(history).toEqual(rows.slice(0, 2));
		expect(live).toEqual(rows.slice(1));
	});
	test("preserves separate copies of identical recorded events", () => {
		const entry = create(LogEntrySchema, { resourceId: "recommendation", body: "repeated" });
		const duplicates = toRows([entry, entry], new Map());
		expect(visibleLogRows(duplicates, duplicates)).toHaveLength(2);
	});
	test("merges distinct instrumentation scopes without repeating a history overlap", () => {
		const entry = create(LogEntrySchema, {
			timestamp: { seconds: 1n },
			resourceId: "recommendation",
			body: "request completed",
			scopeName: "application-sdk",
			scopeVersion: "1.0",
		});
		for (const source of [
			{ scopeName: "stdout-collector", scopeVersion: "1.0" },
			{ scopeName: "application-sdk", scopeVersion: "2.0" },
		]) {
			const other = create(LogEntrySchema, { ...entry, ...source });
			const merged = visibleLogRows(toRows([entry], new Map()), toRows([other, entry], new Map()));
			expect(merged.map((row) => [row.entry.scopeName, row.entry.scopeVersion])).toEqual([
				[source.scopeName, source.scopeVersion],
				["application-sdk", "1.0"],
			]);
		}
	});
});

describe("log JSON", () => {
	const utc = { zone: "utc", clock: "24" } as const;
	const parse = (entry: LogEntry) => {
		const [row] = toRows([entry], new Map());
		return JSON.parse(logJson(row!, utc)) as Record<string, unknown>;
	};
	test("writes the timestamp in the preferred zone", () => {
		const json = parse(create(LogEntrySchema, { timestamp: { seconds: 1n }, body: "hello" }));
		expect(json["timestamp"]).toBe("1970-01-01T00:00:01.000Z");
	});
	test("leaves out ids the line does not carry", () => {
		const json = parse(create(LogEntrySchema, { body: "hello" }));
		expect("trace_id" in json).toBe(false);
		expect("span_id" in json).toBe(false);
		expect(parse(create(LogEntrySchema, { body: "x", traceId: "t1", spanId: "s1" }))).toMatchObject({
			trace_id: "t1",
			span_id: "s1",
		});
	});
});
