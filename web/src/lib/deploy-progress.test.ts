import { expect, test } from "bun:test";
import { DeploymentPhase } from "@gen/loco/deployment/v1/deployment_pb";

import { progressSteps } from "./deploy-progress";

const target = {
	name: "web",
	image: "ghcr.io/acme/web:1",
	min: 2,
	cpu: "250m",
	memory: "512Mi",
	hcPath: "",
	hcInterval: "10",
	resourceId: "r1",
};

test("progressSteps fails the queue step of a region whose deployment did not start", () => {
	const steps = progressSteps({
		target,
		region: "eu-west-1",
		envName: "production",
		phase: undefined,
		message: "",
		error: "region has no cluster",
		submitting: false,
	});
	expect(steps.map((s) => s.state)).toEqual(["done", "failed", "todo", "todo"]);
	expect(steps[1]?.meta).toBe("region has no cluster");
	expect(steps[3]?.meta).toBe("No HTTP health check");
});

test("progressSteps marks every step done once the deployment runs", () => {
	const steps = progressSteps({
		target: { ...target, deploymentId: "d1", hcPath: "/healthz" },
		region: "us-east-1",
		envName: "production",
		phase: DeploymentPhase.RUNNING,
		message: "",
		error: null,
		submitting: false,
	});
	expect(steps.every((s) => s.state === "done")).toBe(true);
	expect(steps[3]?.meta).toBe("/healthz every 10s");
});
