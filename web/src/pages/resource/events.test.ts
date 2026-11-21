import { create } from "@bufbuild/protobuf";
import { describe, expect, test } from "bun:test";
import { EventSchema } from "@gen/loco/event/v1/event_pb";
import { runtimeEvent } from "@/lib/runtime-events";
import { msToTimestamp } from "@/lib/time";
import { eventRows, recentWarningCount } from "./events";

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
					event("restarting", "resource.degraded", 5),
					event("", "resource.recovered", 1),
					event("health_check_failing", "resource.degraded", 59),
					event("restarting", "resource.degraded", 61),
					event("restarting", "resource.degraded", -1),
				],
				NOW_MS,
			),
		).toBe(2);
	});
});

describe("eventRows", () => {
	test("collapses repeats of the same event in a row and keeps the newest", () => {
		const rows = eventRows([
			event("restarting", "resource.degraded", 30),
			event("restarting", "resource.degraded", 20),
			event("", "resource.recovered", 15),
			event("restarting", "resource.degraded", 10),
			event("restarting", "resource.degraded", 5),
		]);
		expect(rows.map((r) => [r.title, r.count])).toEqual([
			["restarting", 2],
			["Service recovered", 1],
			["restarting", 2],
		]);
		expect(rows[0]?.event.id).toBe("p1-5");
	});
	test("names deploy outcomes and marks failures as warnings", () => {
		const failed = runtimeEvent(
			create(EventSchema, {
				id: "d1",
				type: "deployment.failed",
				title: "Deploy failed in us-ca-1",
				severity: "error",
				region: "us-ca-1",
				data: { deploymentId: "0199d1c2-0000-7000-8000-000000000009", regions: ["us-ca-1"] },
				createdAt: msToTimestamp(NOW_MS),
			}),
		);
		const [row] = eventRows([failed]);
		expect(row).toMatchObject({
			title: "Deploy failed in us-ca-1",
			detail: "deployment 00000009",
			glyph: "err",
			warning: true,
		});
	});
});
