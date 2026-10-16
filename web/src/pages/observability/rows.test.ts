import { describe, expect, test } from "bun:test";
import { create } from "@bufbuild/protobuf";
import { LogEntrySchema } from "@gen/loco/observability/v1/observability_pb";

import { toRows, visibleLogRows } from "./rows";

const rows = toRows([1, 2, 3].map((seconds) => create(LogEntrySchema, {
	resourceId: "recommendation", timestamp: { seconds: BigInt(seconds) }, body: `record ${String(seconds)}`,
})), new Map());

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
});
