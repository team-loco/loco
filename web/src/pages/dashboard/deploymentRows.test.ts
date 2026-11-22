import { create } from "@bufbuild/protobuf";
import { describe, expect, test } from "bun:test";
import { DeploymentStatus, DeploymentStatusCountSchema } from "@gen/loco/deployment/v1/deployment_pb";

import { ALL_DEPLOYMENTS, deploymentCounts, deploymentState, filterStatuses } from "./deploymentRows";

function state(status: DeploymentStatus) {
	return deploymentState(status);
}

describe("deploymentState", () => {
	test("the reported phase determines whether a deployment is running or superseded", () => {
		expect(state(DeploymentStatus.RUNNING)).toBe("running");
		expect(state(DeploymentStatus.SUPERSEDED)).toBe("superseded");
		expect(state(DeploymentStatus.FAILED)).toBe("failed");
		expect(state(DeploymentStatus.BUILDING)).toBe("progress");
		expect(state(DeploymentStatus.CANCELED)).toBe("other");
	});

	test("a degraded deployment counts as failed", () => {
		expect(state(DeploymentStatus.DEGRADED)).toBe("failed");
	});
});

describe("filters", () => {
	test("each filter lists the statuses its state groups, and all lists every status", () => {
		expect(filterStatuses("progress")).toEqual([DeploymentStatus.BUILDING, DeploymentStatus.DEPLOYING]);
		expect(filterStatuses(ALL_DEPLOYMENTS)).toEqual([]);
	});

	test("counts each state from per-status counts", () => {
		const counts = deploymentCounts(
			[
				{ status: DeploymentStatus.RUNNING, count: 2 },
				{ status: DeploymentStatus.FAILED, count: 1 },
				{ status: DeploymentStatus.CRASHED, count: 2 },
				{ status: DeploymentStatus.SUPERSEDED, count: 4 },
			].map((c) => create(DeploymentStatusCountSchema, c)),
		);
		expect(counts.get("running")).toBe(2);
		expect(counts.get("failed")).toBe(3);
		expect(counts.get("superseded")).toBe(4);
		expect(counts.get("progress")).toBeUndefined();
	});
});
