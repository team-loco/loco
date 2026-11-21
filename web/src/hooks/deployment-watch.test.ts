import { expect, test } from "bun:test";
import { create } from "@bufbuild/protobuf";
import {
	ActivitySchema,
	DeploymentSchema,
	DeploymentStatus,
	PlacementStatus,
} from "@gen/loco/deployment/v1/deployment_pb";

import { mergeActivity, statusKey } from "./deployment-watch";

test("replayed and overlapping activity batches preserve each position once", () => {
	const a = (seq: bigint) => create(ActivitySchema, { seq, message: `event ${seq}` });
	const previous = [a(1n), a(2n)];
	expect(mergeActivity(previous, [a(2n), a(3n)]).map((entry) => entry.seq)).toEqual([1n, 2n, 3n]);
	expect(mergeActivity(previous, []).map((entry) => entry.seq)).toEqual([1n, 2n]);
	expect(mergeActivity([], [a(4n), a(3n)]).map((entry) => entry.seq)).toEqual([3n, 4n]);
});

test("statusKey changes with the deployment's or a placement's status, not with its replicas", () => {
	const d = (status: DeploymentStatus, placement: PlacementStatus, ready: boolean) =>
		create(DeploymentSchema, {
			status,
			placements: [
				{ region: "us-va-1", status: placement, replicas: [{ ready }] },
				{ region: "us-ca-1", status: PlacementStatus.RUNNING },
			],
		});
	const base = statusKey(d(DeploymentStatus.DEPLOYING, PlacementStatus.DEPLOYING, false));
	expect(statusKey(d(DeploymentStatus.DEPLOYING, PlacementStatus.DEPLOYING, true))).toBe(base);
	expect(statusKey(d(DeploymentStatus.DEPLOYING, PlacementStatus.RUNNING, true))).not.toBe(base);
	expect(statusKey(d(DeploymentStatus.RUNNING, PlacementStatus.DEPLOYING, false))).not.toBe(base);
});
