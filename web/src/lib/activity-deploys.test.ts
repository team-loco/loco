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
	rolloutGroups,
	reportedReadyLabel,
	rolloutReadyLabel,
	tookLabel,
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

test("finishedLabel gives the run time and when it ended", () => {
	const d = deployment("d", DeploymentStatus.RUNNING, 7, { startedAt: timestampFromMs(NOW - 7 * MIN + 59_000) });
	expect(finishedLabel(d, NOW)).toBe("took 59s · 6m ago");
	const now = deployment("d", DeploymentStatus.RUNNING, 1, { startedAt: timestampFromMs(NOW - 10_000) });
	expect(finishedLabel(now, NOW)).toBe("took 50s · just now");
	const superseded = deployment("d", DeploymentStatus.SUPERSEDED, 7, {
		startedAt: timestampFromMs(NOW - 7 * MIN + 59_000),
		completedAt: timestampFromMs(NOW - MIN),
	});
	expect(finishedLabel(superseded, NOW)).toBe("took 59s · 6m ago");
});

test("tookLabel writes units", () => {
	expect([0, 59_000, 60_000, 289_000, 3_600_000, 3_729_000].map(tookLabel)).toEqual([
		"0s",
		"59s",
		"1m",
		"4m 49s",
		"1h",
		"1h 2m",
	]);
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

describe("reportedReadyLabel", () => {
	test("counts the ready replicas the cluster reports for each region of the rollout", () => {
		const east = deployment("east", DeploymentStatus.RUNNING, 1, { desiredReplicas: 2 });
		const west = deployment("west", DeploymentStatus.RUNNING, 1, { desiredReplicas: 2, region: "eu-west-1" });
		const ready = new Map([
			["east", 1],
			["west", 2],
		]);
		expect(reportedReadyLabel([east, west], ready)).toBe("3 of 4 ready in 2 regions");
		expect(reportedReadyLabel([east], ready)).toBe("1 of 2 ready");
	});

	test("says nothing for deployments the cluster does not report on", () => {
		const east = deployment("east", DeploymentStatus.SUPERSEDED, 1, { desiredReplicas: 2 });
		expect(reportedReadyLabel([east], new Map())).toBeUndefined();
	});
});
