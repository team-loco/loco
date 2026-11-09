import { describe, expect, test } from "bun:test";

import { ResourceStatus } from "@gen/loco/resource/v1/resource_pb";

import { demoFrame, REST_FRAME, TERMINAL } from "./deploy";

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
