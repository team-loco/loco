import { describe, expect, test } from "bun:test";
import { DeploymentPhase } from "@gen/loco/deployment/v1/deployment_pb";

import { rolloutSteps } from "./rollout";

function states(phase: DeploymentPhase, ready: number, desired: number) {
	return rolloutSteps(phase, "", ready, desired).map((s) => s.state);
}

describe("rolloutSteps", () => {
	test("waits in the queue while pending", () => {
		expect(states(DeploymentPhase.PENDING, 0, 2)).toEqual(["active", "todo", "todo", "todo"]);
	});

	test("rolls out until every desired replica is ready", () => {
		expect(states(DeploymentPhase.DEPLOYING, 1, 2)).toEqual(["done", "active", "todo", "todo"]);
		expect(states(DeploymentPhase.DEPLOYING, 2, 2)).toEqual(["done", "done", "active", "todo"]);
	});

	test("marks the rollout step failed", () => {
		expect(states(DeploymentPhase.FAILED, 0, 2)).toEqual(["done", "failed", "todo", "todo"]);
	});

	test("finishes once running", () => {
		expect(states(DeploymentPhase.RUNNING, 2, 2)).toEqual(["done", "done", "done", "done"]);
	});

	test("shows the watch message on the active step and the ready count", () => {
		const steps = rolloutSteps(DeploymentPhase.DEPLOYING, "Waiting for replicas", 1, 3);
		expect(steps[1]?.detail).toBe("Waiting for replicas");
		expect(steps[2]?.detail).toBe("1/3 ready");
	});
});
