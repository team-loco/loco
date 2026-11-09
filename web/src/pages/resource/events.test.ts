import { create } from "@bufbuild/protobuf";
import { describe, expect, test } from "bun:test";
import { EventSchema } from "@gen/loco/resource/v1/resource_pb";

import { msToTimestamp } from "@/lib/time";

import { latestBackOff, recentWarningCount } from "./events";

const NOW_MS = Date.UTC(2026, 10, 8, 12, 0, 0);
const MINUTE_MS = 60_000;

function event(reason: string, type: string, minutesAgo: number, podName = "") {
	return create(EventSchema, { reason, type, podName, timestamp: msToTimestamp(NOW_MS - minutesAgo * MINUTE_MS) });
}

describe("recentWarningCount", () => {
	test("counts warnings from the last hour only", () => {
		const events = [
			event("BackOff", "Warning", 5),
			event("Pulled", "Normal", 5),
			event("Unhealthy", "Warning", 59),
			event("BackOff", "Warning", 61),
		];
		expect(recentWarningCount(events, NOW_MS)).toBe(2);
	});
});

describe("latestBackOff", () => {
	test("picks the newest BackOff that names a replica", () => {
		const events = [
			event("BackOff", "Warning", 30, "shop-a"),
			event("BackOff", "Warning", 2, ""),
			event("BackOff", "Warning", 10, "shop-b"),
		];
		expect(latestBackOff(events)?.podName).toBe("shop-b");
	});
});
