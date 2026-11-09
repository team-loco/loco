import { describe, expect, test } from "bun:test";

import { ResourceStatus } from "@gen/loco/resource/v1/resource_pb";

import { demoFrame, REST_FRAME, rolloutLimits, simulateRollout, TERMINAL } from "./deploy";

describe("rolloutLimits", () => {
	test("rounds surge up and unavailability down, like a 25% rolling update", () => {
		expect(rolloutLimits(4)).toEqual({ maxSurge: 1, maxUnavailable: 1 });
		expect(rolloutLimits(3)).toEqual({ maxSurge: 1, maxUnavailable: 0 });
		expect(rolloutLimits(10)).toEqual({ maxSurge: 3, maxUnavailable: 2 });
	});
});

describe("simulateRollout", () => {
	for (const replicas of [1, 3, 4, 10]) {
		test(`keeps ${replicas} replicas within the surge and availability limits`, () => {
			const { maxSurge, maxUnavailable } = rolloutLimits(replicas);
			const stages = simulateRollout(replicas);
			for (const pods of stages) {
				const live = pods.filter((p) => p.phase !== "terminating");
				const ready = pods.filter((p) => p.phase === "ready");
				expect(live.length).toBeLessThanOrEqual(replicas + maxSurge);
				expect(ready.length).toBeGreaterThanOrEqual(replicas - maxUnavailable);
			}
			const last = stages.at(-1) ?? [];
			expect(last).toHaveLength(replicas);
			expect(last.every((p) => p.fresh && p.phase === "ready")).toBe(true);
		});
	}
});

describe("demoFrame", () => {
	test("starts healthy on the old pods and settles healthy on the new ones", () => {
		const first = demoFrame(0);
		expect(first.status).toBe(ResourceStatus.HEALTHY);
		expect(first.regions.flatMap((r) => r.pods).some((p) => p.fresh)).toBe(false);

		const rest = demoFrame(REST_FRAME);
		expect(rest.status).toBe(ResourceStatus.HEALTHY);
		expect(rest.ready).toBe(rest.desired);
		expect(rest.shown).toBe(TERMINAL.length);
		expect(rest.regions.flatMap((r) => r.pods).every((p) => p.fresh && p.phase === "ready")).toBe(true);
	});

	test("reports deploying while a region is still rolling out", () => {
		expect(demoFrame(REST_FRAME - 1).status).toBe(ResourceStatus.DEPLOYING);
	});
});
