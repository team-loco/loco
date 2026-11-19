import { expect, test } from "bun:test";
import { create } from "@bufbuild/protobuf";
import { LogBucketSchema, LogLevel } from "@gen/loco/observability/v1/observability_pb";
import { msToTimestamp } from "@/lib/time";
import { logVolume } from "./log-volume";

test("volume fills aligned buckets over the full range", () => {
	const bucket = (start: number, level: LogLevel, count: bigint) =>
		create(LogBucketSchema, { startTime: msToTimestamp(start), level, count });
	const volume = logVolume(
		[bucket(0, LogLevel.INFO, 10005n), bucket(0, LogLevel.INFO, 2n), bucket(2000, LogLevel.ERROR, 3n)],
		500,
		2500,
		1000,
	);
	expect(volume.points.map((point) => point.start)).toEqual([0, 1000, 2000]);
	expect(volume.points[0]?.counts.get("info")).toBe(10007);
	expect(volume.points[1]?.counts.size).toBe(0);
	expect(volume.points[2]?.counts.get("error")).toBe(3);
	expect(volume.totals.get("info")).toBe(10007);
	expect(volume.totals.get("error")).toBe(3);
});
