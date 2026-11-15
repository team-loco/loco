import { create } from "@bufbuild/protobuf";
import { timestampFromMs } from "@bufbuild/protobuf/wkt";
import { BuildSchema, BuildStatus } from "@gen/loco/build/v1/build_pb";
import { DeploymentSchema, DeploymentPhase } from "@gen/loco/deployment/v1/deployment_pb";
import { expect, test } from "bun:test";

import { deploymentSteps, progressDeployments } from "./deployment-progress";
import type { RegionView } from "./model";

const previous = create(DeploymentSchema, {
	id: "previous",
	region: "east",
	status: DeploymentPhase.RUNNING,
	isActive: true,
	createdAt: timestampFromMs(1000),
	spec: { spec: { case: "service", value: { build: { type: "image", image: "example.test/app:v1" } } } },
});
const pending = create(DeploymentSchema, {
	id: "pending",
	region: "east",
	status: DeploymentPhase.PENDING,
	createdAt: timestampFromMs(3000),
});
const region: RegionView = { name: "east", history: [pending, previous], current: previous, status: undefined };

test("a new pending deployment appears while the region still runs its previous release", () => {
	const [progress] = progressDeployments([region]);
	expect(progress?.deployment.id).toBe("pending");
	expect(progress?.previous?.id).toBe("previous");
});

test("a failed newest deployment offers the previous running release", () => {
	const failed = create(DeploymentSchema, { ...pending, status: DeploymentPhase.FAILED });
	expect(progressDeployments([{ ...region, history: [failed, previous] }])[0]?.previous?.id).toBe("previous");
	const running = create(DeploymentSchema, { ...pending, status: DeploymentPhase.RUNNING });
	expect(progressDeployments([{ ...region, history: [running, previous] }])).toEqual([]);
});

test("progress does not use a sibling region's deployments", () => {
	const other = create(DeploymentSchema, {
		...previous,
		id: "other",
		region: "west",
		createdAt: timestampFromMs(4000),
	});
	expect(
		progressDeployments([region, { name: "west", history: [other], current: other, status: undefined }]).map(
			(p) => p.deployment.id,
		),
	).toEqual(["pending"]);
});

test("queued, deploying and running follow deployment phases", () => {
	const states = (status: DeploymentPhase) =>
		deploymentSteps(create(DeploymentSchema, { ...pending, status }), undefined).map((s) => s.state);
	expect(states(DeploymentPhase.PENDING)).toEqual(["active", "todo", "todo"]);
	expect(states(DeploymentPhase.DEPLOYING)).toEqual(["done", "active", "todo"]);
	expect(states(DeploymentPhase.RUNNING)).toEqual(["done", "done", "done"]);
	expect(states(DeploymentPhase.CANCELED)).toEqual(["done", "failed", "todo"]);
});

test("a failed deployment marks the deployment phase and leaves Running incomplete", () => {
	const failed = create(DeploymentSchema, {
		...pending,
		status: DeploymentPhase.FAILED,
	});
	expect(deploymentSteps(failed, undefined)).toEqual([
		{ label: "Queued", detail: "", state: "done" },
		{ label: "Deploying", detail: "", state: "failed" },
		{ label: "Running", detail: "", state: "todo" },
	]);
});

test("a source build adds its recorded branch and duration", () => {
	const build = create(BuildSchema, {
		status: BuildStatus.SUCCEEDED,
		gitBranch: "main",
		startedAt: timestampFromMs(1000),
		finishedAt: timestampFromMs(69000),
	});
	expect(deploymentSteps(pending, build)[0]).toEqual({
		label: "Build",
		detail: "main · 1m 08s · Succeeded",
		state: "done",
	});
});
