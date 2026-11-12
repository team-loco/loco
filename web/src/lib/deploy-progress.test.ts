import { expect, test } from "bun:test";
import { DeploymentPhase } from "@gen/loco/deployment/v1/deployment_pb";

import { regionDeploySteps, runOutcome, type RegionProgress } from "./deploy-progress";

const progress: RegionProgress = {
	form: {
		image: "ghcr.io/acme/web:1",
		name: "web",
		description: "",
		regions: ["us-east-1", "eu-west-1"],
		cpu: "250m",
		memory: "512Mi",
		replicas: 2,
		maxReplicas: 2,
		cpuTarget: 70,
		isPublic: false,
		subdomain: "",
		platformDomain: "",
		port: "8080",
		health: { path: "", interval: "10", timeout: "3", failAfter: "3" },
	},
	created: true,
	region: "us-east-1",
	phase: undefined,
	message: "",
	error: null,
};

const states = (p: Partial<RegionProgress>) => regionDeploySteps({ ...progress, ...p }).map((s) => s.state);

test("regionDeploySteps waits on nothing until the resource exists", () => {
	expect(states({ created: false })).toEqual(["todo", "todo", "todo", "todo", "todo"]);
});

test("regionDeploySteps pulls while queued and starts replicas while deploying", () => {
	expect(states({ phase: DeploymentPhase.PENDING })).toEqual(["active", "todo", "todo", "todo", "todo"]);
	const deploying = regionDeploySteps({
		...progress,
		phase: DeploymentPhase.DEPLOYING,
		message: "Scheduling deployment",
	});
	expect(deploying.map((s) => s.state)).toEqual(["active", "active", "todo", "todo", "todo"]);
	expect(deploying.map((s) => s.meta).slice(0, 2)).toEqual(["web:1", "Scheduling deployment"]);
});

test("regionDeploySteps leaves every step to do and adds one failed line", () => {
	const steps = regionDeploySteps({ ...progress, error: "region has no cluster" });
	expect(steps.map((s) => s.state)).toEqual(["todo", "todo", "todo", "todo", "todo", "failed"]);
	expect(steps.at(-1)).toEqual({ label: "Failed", state: "failed", meta: "region has no cluster" });
	const failed = regionDeploySteps({ ...progress, phase: DeploymentPhase.FAILED, message: "image pull backoff" });
	expect(failed.at(-1)).toEqual({ label: "Failed", state: "failed", meta: "image pull backoff" });
	const canceled = regionDeploySteps({ ...progress, phase: DeploymentPhase.CANCELED, message: "superseded" });
	expect(canceled.at(-1)).toEqual({ label: "Canceled", state: "failed", meta: "superseded" });
});

test("regionDeploySteps marks every step done once the deployment runs", () => {
	const steps = regionDeploySteps({
		...progress,
		form: {
			...progress.form,
			isPublic: true,
			subdomain: "web",
			platformDomain: "apps.test",
			health: { ...progress.form.health, path: "/healthz" },
		},
		phase: DeploymentPhase.RUNNING,
	});
	expect(steps.map((s) => [s.label, s.state, s.meta])).toEqual([
		["Pull image", "done", "web:1"],
		["Start replicas", "done", "2 × 250m CPU · 512Mi"],
		["Health checks", "done", "/healthz · every 10s"],
		["Route traffic", "done", "web.apps.test"],
		["Live", "done", "us-east-1"],
	]);
	expect(regionDeploySteps(progress)[2]?.meta).toBe("No HTTP health check");
});

test("runOutcome is failed only once every region failed", () => {
	const live = regionDeploySteps({ ...progress, phase: DeploymentPhase.RUNNING });
	const failed = regionDeploySteps({ ...progress, phase: DeploymentPhase.FAILED, message: "image pull backoff" });
	const rejected = regionDeploySteps({ ...progress, error: "region has no cluster" });
	const pending = regionDeploySteps({ ...progress, phase: DeploymentPhase.PENDING });
	expect(runOutcome([failed, rejected])).toBe("failed");
	expect(runOutcome([live, live])).toBe("live");
	expect(runOutcome([failed, pending])).toBe("deploying");
	expect(runOutcome([])).toBe("deploying");
});
