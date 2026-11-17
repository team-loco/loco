import { expect, test } from "bun:test";
import { DeploymentStatus } from "@gen/loco/deployment/v1/deployment_pb";

import { runOutcome } from "./deploy-progress";

test("the deploy drawer waits for every region's outcome", () => {
	const live = { phase: DeploymentStatus.RUNNING, error: null };
	const failed = { phase: DeploymentStatus.FAILED, error: null };
	const rejected = { phase: undefined, error: "region has no cluster" };
	const pending = { phase: DeploymentStatus.BUILDING, error: null };
	expect(runOutcome([failed, rejected])).toBe("failed");
	expect(runOutcome([live, live])).toBe("live");
	expect(runOutcome([failed, pending])).toBe("deploying");
	expect(runOutcome([failed, live])).toBe("deploying");
	expect(runOutcome([{ phase: DeploymentStatus.CANCELED, error: null }])).toBe("failed");
	expect(runOutcome([{ phase: DeploymentStatus.SUPERSEDED, error: null }])).toBe("live");
	expect(runOutcome([])).toBe("deploying");
});
