import { describe, expect, test } from "bun:test";
import { create } from "@bufbuild/protobuf";
import { timestampFromMs } from "@bufbuild/protobuf/wkt";
import { DeploymentSchema, DeploymentStatus } from "@gen/loco/deployment/v1/deployment_pb";
import { ResourceStatus } from "@gen/loco/resource/v1/resource_pb";

import {
	activityDeployments,
	deployPhase,
	deploySteps,
	finishedLabel,
	readyLabel,
	rolloutGroups,
	rolloutReadyLabel,
	withoutRunningDeploys,
} from "./activity-deploys";
import type { Alert } from "./alerts";

const NOW = 1_800_000_000_000;
const MIN = 60_000;

function deployment(id: string, status: DeploymentStatus, agoMin: number, extra: object = {}) {
	return create(DeploymentSchema, {
		id,
		resourceId: `r-${id}`,
		region: "us-east-1",
		status,
		createdAt: timestampFromMs(NOW - agoMin * MIN),
		...extra,
	});
}

describe("activityDeployments", () => {
	test("keeps in-flight and recent deployments, running first, then failed, live and canceled, newest first", () => {
		const list = [
			deployment("old-live", DeploymentStatus.RUNNING, 45),
			deployment("old-run", DeploymentStatus.DEPLOYING, 50),
			deployment("canceled", DeploymentStatus.CANCELED, 2),
			deployment("live-new", DeploymentStatus.RUNNING, 1),
			deployment("live", DeploymentStatus.RUNNING, 10),
			deployment("failed", DeploymentStatus.FAILED, 20),
			deployment("build", DeploymentStatus.BUILDING, 3),
		];
		expect(activityDeployments(list, NOW).map((d) => d.id)).toEqual([
			"build",
			"old-run",
			"failed",
			"live-new",
			"live",
			"canceled",
		]);
	});

	test("shows at most six", () => {
		const list = Array.from({ length: 9 }, (_, i) => deployment(`d${i.toString()}`, DeploymentStatus.RUNNING, i));
		expect(activityDeployments(list, NOW)).toHaveLength(6);
	});
});

describe("deployPhase", () => {
	test.each([
		[DeploymentStatus.BUILDING, false, "Building"],
		[DeploymentStatus.DEPLOYING, true, "Failing, retrying"],
		[DeploymentStatus.RUNNING, false, "Deployed"],
		[DeploymentStatus.RUNNING, true, "Running"],
		[DeploymentStatus.FAILED, true, "Failed"],
		[DeploymentStatus.CANCELED, false, "Canceled"],
		[DeploymentStatus.UNSPECIFIED, false, "Queued"],
	])("%i with problem %p reads %s", (status, problem, label) => {
		const d = deployment("d", status, 1, problem ? { problem: { reason: "crash_loop", message: "x" } } : {});
		expect(deployPhase(d).label).toBe(label);
	});
});

describe("deploySteps", () => {
	test("a build is the active step while building", () => {
		expect(deploySteps(deployment("d", DeploymentStatus.BUILDING, 1))).toEqual({
			build: "active",
			deploy: "pending",
			current: "build",
		});
	});

	test("an image deploy skips the build step", () => {
		expect(deploySteps(deployment("d", DeploymentStatus.DEPLOYING, 1))).toEqual({
			build: "skipped",
			deploy: "active",
			current: "deploy",
		});
	});

	test("a problem marks the active step as retrying", () => {
		const d = deployment("d", DeploymentStatus.DEPLOYING, 1, { problem: { reason: "crash_loop" } });
		expect(deploySteps(d).deploy).toBe("retry");
	});
});

test("readyLabel counts ready replicas of this deployment", () => {
	const d = deployment("d", DeploymentStatus.RUNNING, 1, {
		desiredReplicas: 2,
		replicas: [
			{ ready: true, deploymentId: "d" },
			{ ready: true, deploymentId: "old" },
			{ ready: false, deploymentId: "d" },
		],
	});
	expect(readyLabel(d)).toBe("1 of 2 ready");
});

test("finishedLabel gives the run time and when it ended", () => {
	const d = deployment("d", DeploymentStatus.RUNNING, 7, { completedAt: timestampFromMs(NOW - 7 * MIN + 59_000) });
	expect(finishedLabel(d, NOW)).toBe("took 0:59 · 6m ago");
	const now = deployment("d", DeploymentStatus.RUNNING, 1, { completedAt: timestampFromMs(NOW - 10_000) });
	expect(finishedLabel(now, NOW)).toBe("took 0:50 · just now");
});

test("withoutRunningDeploys drops rollout alerts a deploy card covers", () => {
	const alert = (resourceId: string, status: ResourceStatus): Alert => ({
		key: `${resourceId}/us-east-1`,
		resourceId,
		name: resourceId,
		description: "",
		region: "us-east-1",
		status,
		message: "",
		reportedMs: undefined,
	});
	const alerts = [
		alert("r-a", ResourceStatus.DEPLOYING),
		alert("r-b", ResourceStatus.DEPLOYING),
		alert("r-a", ResourceStatus.DEGRADED),
	];
	const kept = withoutRunningDeploys(alerts, [deployment("a", DeploymentStatus.DEPLOYING, 1)]);
	expect(kept.map((a) => `${a.resourceId}:${a.status.toString()}`)).toEqual([
		`r-b:${ResourceStatus.DEPLOYING.toString()}`,
		`r-a:${ResourceStatus.DEGRADED.toString()}`,
	]);
});

describe("rolloutGroups", () => {
	test("groups the regions of one rollout of a resource and keeps the order of first appearance", () => {
		const east = deployment("east", DeploymentStatus.DEPLOYING, 1, { resourceId: "r", rolloutId: "a" });
		const other = deployment("other", DeploymentStatus.RUNNING, 2, { resourceId: "s", rolloutId: "b" });
		const west = deployment("west", DeploymentStatus.DEPLOYING, 1, {
			resourceId: "r",
			rolloutId: "a",
			region: "eu-west-1",
		});
		const groups = rolloutGroups([east, other, west]);
		expect(groups.map((group) => group.map((d) => d.id))).toEqual([["east", "west"], ["other"]]);
	});

	test("keeps deployments of separate rollouts apart", () => {
		const first = deployment("first", DeploymentStatus.RUNNING, 5, { resourceId: "r", rolloutId: "a" });
		const second = deployment("second", DeploymentStatus.DEPLOYING, 1, { resourceId: "r", rolloutId: "b" });
		expect(rolloutGroups([second, first]).map((group) => group.map((d) => d.id))).toEqual([["second"], ["first"]]);
	});

	test("keeps resources apart even when their rollout IDs match", () => {
		const web = deployment("web", DeploymentStatus.DEPLOYING, 1, { resourceId: "web", rolloutId: "a" });
		const api = deployment("api", DeploymentStatus.DEPLOYING, 1, { resourceId: "api", rolloutId: "a" });
		expect(rolloutGroups([web, api]).map((group) => group.map((d) => d.id))).toEqual([["web"], ["api"]]);
	});
});

describe("rolloutReadyLabel", () => {
	test("counts the ready replicas of every region of the rollout", () => {
		const replica = (deploymentId: string, ready: boolean) => ({ deploymentId, ready });
		const east = deployment("east", DeploymentStatus.DEPLOYING, 1, {
			desiredReplicas: 2,
			replicas: [replica("east", true), replica("east", false)],
		});
		const west = deployment("west", DeploymentStatus.DEPLOYING, 1, {
			desiredReplicas: 2,
			region: "eu-west-1",
			replicas: [replica("west", true), replica("west", true), replica("old", true)],
		});
		expect(rolloutReadyLabel([east, west])).toBe("3 of 4 ready in 2 regions");
		expect(rolloutReadyLabel([east])).toBe("1 of 2 ready");
	});
});
