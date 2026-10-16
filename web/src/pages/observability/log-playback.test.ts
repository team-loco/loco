import { expect, test } from "bun:test";
import { create } from "@bufbuild/protobuf";
import { LogEntrySchema } from "@gen/loco/observability/v1/observability_pb";

import { completeLogResume, logPlaybackForKey, type LogPlayback } from "./log-playback";
import { toRows } from "./rows";

const paused = { rows: toRows([create(LogEntrySchema, { body: "received live row" })], new Map()),
	olderCursor: "older-page", newerCursor: "newer-page" };

test("keeps a pending resume through the anchored URL transition and then enables Live", () => {
	const pending = { from: "anchored", to: "latest" };
	const initial: LogPlayback = { key: "anchored", on: false, pending, paused };
	expect(logPlaybackForKey(initial, "anchored")).toBe(initial);
	const navigated = logPlaybackForKey(initial, "latest");
	expect(navigated.pending).toBe(pending);
	expect(navigated.paused).toBe(paused);
	const completed = completeLogResume(navigated, pending, true);
	expect(completed.on).toBe(true);
	expect(completed.pending).toBeNull();
	expect(completed.paused).toBeNull();
});

test("keeps both paused cursors and rows when the latest refresh fails", () => {
	const pending = { from: "anchored", to: "latest" };
	const initial: LogPlayback = { key: "anchored", on: false, pending, paused };
	const failed = completeLogResume(logPlaybackForKey(initial, "latest"), pending, false);
	expect(failed.on).toBe(false);
	expect(failed.pending).toBeNull();
	expect(failed.paused).toBe(paused);
	expect(failed.paused?.olderCursor).toBe("older-page");
	expect(failed.paused?.newerCursor).toBe("newer-page");
});

test("ignores a completed resume after filters or history changed", () => {
	for (const key of ["another-workspace", "another-environment", "filtered", "older-cursor", "newer-cursor"]) {
		const pending = { from: "anchored", to: "latest" };
		const initial: LogPlayback = { key: "anchored", on: false, pending, paused };
		const changed = logPlaybackForKey(initial, key);
		expect(changed.pending).toBeNull();
		expect(changed.paused).toBeNull();
		expect(completeLogResume(changed, pending, true)).toBe(changed);
		expect(completeLogResume(changed, pending, false)).toBe(changed);
		expect(changed.on).toBe(false);
	}
});

test("cancels pending Live when Back restores the anchored URL", () => {
	const pending = { from: "anchored", to: "latest" };
	const initial: LogPlayback = { key: "anchored", on: false, pending, paused };
	const latest = logPlaybackForKey(initial, "latest");
	const restored = logPlaybackForKey(latest, "anchored");
	expect(restored.pending).toBeNull();
	expect(restored.on).toBe(false);
	expect(completeLogResume(restored, pending, true)).toBe(restored);
});
