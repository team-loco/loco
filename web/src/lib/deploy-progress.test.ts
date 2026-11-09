import { expect, test } from "bun:test";
import { DeploymentPhase } from "@gen/loco/deployment/v1/deployment_pb";

import { createdStep, regionDeploySteps } from "./deploy-progress";
import type { NewResourceForm } from "./new-resource";

const form: NewResourceForm = {
	image: "ghcr.io/acme/web:1",
	name: "web",
	regions: ["us-east-1", "eu-west-1"],
	cpu: "250m",
	memory: "512Mi",
	replicas: 2,
	isPublic: false,
	subdomain: "",
	platformDomain: "",
	port: "8080",
	envText: "",
	healthPath: "",
};

test("createdStep is active while submitting and done once the resource exists", () => {
	expect(createdStep("web", "production", false, true).state).toBe("active");
	expect(createdStep("web", "production", true, false)).toEqual({
		label: "Resource created",
		state: "done",
		meta: "web · production",
	});
});

test("regionDeploySteps fails the queue step of a region whose deployment did not start", () => {
	const steps = regionDeploySteps({
		form,
		hcInterval: "10",
		created: true,
		deploymentId: undefined,
		region: "eu-west-1",
		phase: undefined,
		message: "",
		error: "region has no cluster",
	});
	expect(steps.map((s) => s.state)).toEqual(["failed", "todo", "todo"]);
	expect(steps[0]?.meta).toBe("region has no cluster");
	expect(steps[2]?.meta).toBe("No HTTP health check");
});

test("regionDeploySteps marks every step done once the deployment runs", () => {
	const steps = regionDeploySteps({
		form: { ...form, healthPath: "/healthz" },
		hcInterval: "10",
		created: true,
		deploymentId: "d1",
		region: "us-east-1",
		phase: DeploymentPhase.RUNNING,
		message: "",
		error: null,
	});
	expect(steps.every((s) => s.state === "done")).toBe(true);
	expect(steps[2]?.meta).toBe("/healthz every 10s");
});
