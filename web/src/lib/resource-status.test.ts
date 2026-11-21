import { describe, expect, test } from "bun:test";
import { create } from "@bufbuild/protobuf";
import { PlacementStatus } from "@gen/loco/deployment/v1/deployment_pb";
import { RegionStatusSchema } from "@gen/loco/resource/v1/resource_pb";

import { rolloutState } from "./resource-status";

function region(placementStatus: PlacementStatus, readyReplicas: number) {
	return create(RegionStatusSchema, { region: "us-east-1", placementStatus, readyReplicas, desiredReplicas: 2 });
}

describe("rolloutState", () => {
	test("a serving region rolling out a newer deployment is updating", () => {
		expect(rolloutState([region(PlacementStatus.DEPLOYING, 2)])).toBe("updating");
		expect(rolloutState([region(PlacementStatus.PENDING, 1)])).toBe("updating");
	});

	test("a stopped deployment over serving replicas is a failed update", () => {
		for (const status of [PlacementStatus.FAILED, PlacementStatus.CRASHED]) {
			expect(rolloutState([region(status, 2)])).toBe("failed");
		}
	});

	test("a failed update outranks a rollout in another region", () => {
		expect(rolloutState([region(PlacementStatus.DEPLOYING, 2), region(PlacementStatus.FAILED, 2)])).toBe("failed");
	});

	test("nothing marks a settled region or one that serves nothing", () => {
		expect(rolloutState([region(PlacementStatus.RUNNING, 2)])).toBeUndefined();
		expect(rolloutState([region(PlacementStatus.DEGRADED, 1)])).toBeUndefined();
		expect(rolloutState([region(PlacementStatus.DEPLOYING, 0)])).toBeUndefined();
		expect(rolloutState([region(PlacementStatus.FAILED, 0)])).toBeUndefined();
		expect(rolloutState([])).toBeUndefined();
	});
});
