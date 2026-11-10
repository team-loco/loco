import { create } from "@bufbuild/protobuf";
import { describe, expect, test } from "bun:test";
import { EventSchema } from "@gen/loco/event/v1/event_pb";
import { runtimeEvent } from "@/lib/runtime-events";
import { msToTimestamp } from "@/lib/time";
import { latestBackOff, recentWarningCount } from "./events";

const NOW_MS = Date.UTC(2026, 10, 8, 12);
function event(reason: string, type: string, minutesAgo: number, placementId = "p1") {
	return runtimeEvent(
		create(EventSchema, {
			id: `${placementId}-${minutesAgo.toString()}`,
			type,
			data: { reason, placementId },
			createdAt: msToTimestamp(NOW_MS - minutesAgo * 60_000),
		}),
	);
}
describe("runtime events", () => {
	test("counts degradation transitions in the last hour", () => {
		expect(
			recentWarningCount(
				[
					event("crash_looping", "resource.degraded", 5),
					event("", "resource.recovered", 1),
					event("health_check_failing", "resource.degraded", 59),
					event("crash_looping", "resource.degraded", 61),
					event("crash_looping", "resource.degraded", -1),
				],
				NOW_MS,
			),
		).toBe(2);
	});
	test("recovery and a changed problem clear the crash card per placement", () => {
		const crash = event("crash_looping", "resource.degraded", 30);
		expect(latestBackOff([crash, event("", "resource.recovered", 2)])).toBeUndefined();
		expect(latestBackOff([crash, event("image_pull_failed", "resource.degraded", 2)])).toBeUndefined();
		expect(latestBackOff([crash, event("", "resource.recovered", 2, "p2")])).toBe(crash);
	});
});
