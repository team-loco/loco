import { expect, test } from "bun:test";
import { LogOrder } from "@gen/loco/observability/v1/observability_pb";

import { createLogHistory, logAnchor, moveLogHistory } from "./log-history";

test("walks older and newer batches without losing the centered batch", () => {
	const centered = createLogHistory();
	const older = moveLogHistory(centered, "older", "before-target");
	expect(older.current).toEqual({ cursor: "before-target", order: LogOrder.NEWEST_FIRST });
	const oldest = moveLogHistory(older, "older", "further-before");
	const back = moveLogHistory(oldest, "newer", "");
	expect(back.current).toEqual(older.current);
	const middle = moveLogHistory(back, "newer", "");
	expect(middle.current).toEqual(centered.current);
	const newer = moveLogHistory(middle, "newer", "after-target");
	expect(newer.current).toEqual({ cursor: "after-target", order: LogOrder.OLDEST_FIRST });
	expect(moveLogHistory(newer, "older", "").current).toEqual(centered.current);
	expect(centered.older).toEqual([]);
	expect(centered.newer).toEqual([]);
});

test("does not navigate beyond an exhausted result", () => {
	for (const direction of ["older", "newer"] as const) {
		expect(() => moveLogHistory(createLogHistory(), direction, "")).toThrow("No log batch");
	}
});

test("restores a shared focus timestamp without accepting invalid bounds", () => {
	expect(logAnchor("1792130909123")).toBe(1792130909123);
	for (const value of [null, "", "NaN", "Infinity", "-1", "0", "1.5", "9007199254740992"]) {
		expect(logAnchor(value)).toBeNull();
	}
});
