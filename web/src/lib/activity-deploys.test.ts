import { describe, expect, test } from "bun:test";
import { create } from "@bufbuild/protobuf";
import { timestampFromMs } from "@bufbuild/protobuf/wkt";
import { DeploymentSchema, DeploymentStatus, PlacementStatus } from "@gen/loco/deployment/v1/deployment_pb";
import { ResourceSchema, ResourceStatus } from "@gen/loco/resource/v1/resource_pb";

import {
	activityDeployments,
	activityEvents,
	deployPhase,
	deploySteps,
	finishedLabel,
	reportedReadyLabel,
	cancelable,
	canceledNote,
	placementReadyLabel,
	rolloutReadyLabel,
	rollingOutRegions,
	runningDeployRegions,
	tookLabel,
	withoutRunningDeploys,
} from "./activity-deploys";
import type { Alert } from "./alerts";
import { formatShortId } from "@/lib/utils";

const NOW = 1_800_000_000_000;
const MIN = 60_000;

function deployment(id: string, status: DeploymentStatus, agoMin: number, extra: object = {}, placement: object = {}) {
	return create(DeploymentSchema, {
		id,
		resourceId: `r-${id}`,
		placements: [{ region: "us-va-1", ...placement }],
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

	test("lists every deployment, however many", () => {
		const list = Array.from({ length: 17 }, (_, i) => deployment(`d${i.toString()}`, DeploymentStatus.RUNNING, i));
		expect(activityDeployments(list, NOW)).toHaveLength(17);
	});
});

describe("activityEvents", () => {
	const at = (severity: string, minutesAgo: number) => ({
		severity,
		timestamp: timestampFromMs(NOW - minutesAgo * MIN),
	});
	const events = [at("info", 5), at("warning", 20), at("error", 45), at("info", 1)];

	test("lists every event newest first at the all level", () => {
		expect(activityEvents(events, "all").map((e) => e.severity)).toEqual(["info", "info", "warning", "error"]);
	});

	test("keeps only warnings and errors at the warnings level", () => {
		expect(activityEvents(events, "warn").map((e) => e.severity)).toEqual(["warning", "error"]);
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
		const d = deployment("d", status, 1, {}, problem ? { problem: { reason: "restarting", message: "x" } } : {});
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
		const d = deployment("d", DeploymentStatus.DEPLOYING, 1, {}, { problem: { reason: "restarting" } });
		expect(deploySteps(d).deploy).toBe("retry");
	});
});

test("rolloutReadyLabel counts ready replicas of this deployment", () => {
	const d = deployment(
		"d",
		DeploymentStatus.RUNNING,
		1,
		{},
		{
			desiredReplicas: 2,
			replicas: [
				{ ready: true, deploymentId: "d" },
				{ ready: true, deploymentId: "old" },
				{ ready: false, deploymentId: "d" },
			],
		},
	);
	expect(rolloutReadyLabel(d)).toBe("1 of 2 ready");
});

test("finishedLabel gives the run time and when it ended", () => {
	const d = deployment("d", DeploymentStatus.RUNNING, 7, { startedAt: timestampFromMs(NOW - 7 * MIN + 59_000) });
	expect(finishedLabel(d, NOW)).toBe("took 59s, 6m ago");
	const now = deployment("d", DeploymentStatus.RUNNING, 1, { startedAt: timestampFromMs(NOW - 10_000) });
	expect(finishedLabel(now, NOW)).toBe("took 50s, just now");
	const superseded = deployment("d", DeploymentStatus.SUPERSEDED, 7, {
		startedAt: timestampFromMs(NOW - 7 * MIN + 59_000),
		completedAt: timestampFromMs(NOW - MIN),
	});
	expect(finishedLabel(superseded, NOW)).toBe("took 59s, 6m ago");
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
		key: `${resourceId}/us-va-1`,
		resourceId,
		name: resourceId,
		slug: resourceId,
		description: "",
		region: "us-va-1",
		status,
		rollout: undefined,
		message: "",
		sinceMs: undefined,
	});
	const alerts = [
		alert("r-a", ResourceStatus.DEPLOYING),
		alert("r-b", ResourceStatus.DEPLOYING),
		alert("r-a", ResourceStatus.DEGRADED),
	];
	const expected = [`r-b:${ResourceStatus.DEPLOYING.toString()}`, `r-a:${ResourceStatus.DEGRADED.toString()}`];
	const summary = (kept: Alert[]) => kept.map((a) => `${a.resourceId}:${a.status.toString()}`);
	const running = runningDeployRegions([
		deployment("a", DeploymentStatus.DEPLOYING, 1),
		deployment("b", DeploymentStatus.RUNNING, 1),
	]);
	expect(summary(withoutRunningDeploys(alerts, running))).toEqual(expected);
	const resource = (id: string, placementStatus: PlacementStatus) =>
		create(ResourceSchema, {
			id,
			environmentStatuses: [
				{ environmentId: "env", regions: [{ region: "us-va-1", placementStatus }] },
				{ environmentId: "other", regions: [{ region: "us-va-1", placementStatus: PlacementStatus.PENDING }] },
			],
		});
	const rolling = rollingOutRegions(
		[resource("r-a", PlacementStatus.PENDING), resource("r-b", PlacementStatus.RUNNING)],
		"env",
	);
	expect(summary(withoutRunningDeploys(alerts, rolling))).toEqual(expected);
});

describe("rolloutReadyLabel", () => {
	test("counts the ready replicas of every region of the deployment", () => {
		const replica = (deploymentId: string, ready: boolean) => ({ deploymentId, ready });
		const east = { region: "us-va-1", desiredReplicas: 2, replicas: [replica("d", true), replica("d", false)] };
		const west = {
			region: "us-ca-1",
			desiredReplicas: 2,
			replicas: [replica("d", true), replica("d", true), replica("old", true)],
		};
		const both = deployment("d", DeploymentStatus.DEPLOYING, 1, { placements: [east, west] });
		expect(rolloutReadyLabel(both)).toBe("3 of 4 ready in 2 regions");
		const one = deployment("d", DeploymentStatus.DEPLOYING, 1, { placements: [east] });
		expect(rolloutReadyLabel(one)).toBe("1 of 2 ready");
	});
});

describe("reportedReadyLabel", () => {
	test("counts the ready replicas the clusters report for the deployment's regions", () => {
		const east = { region: "us-va-1", desiredReplicas: 2 };
		const west = { region: "us-ca-1", desiredReplicas: 2 };
		const both = deployment("d", DeploymentStatus.RUNNING, 1, { placements: [east, west] });
		expect(reportedReadyLabel(both, new Map([["d", 3]]))).toBe("3 of 4 ready in 2 regions");
		const one = deployment("d", DeploymentStatus.RUNNING, 1, { placements: [east] });
		expect(reportedReadyLabel(one, new Map([["d", 1]]))).toBe("1 of 2 ready");
	});

	test("says nothing for deployments the cluster does not report on", () => {
		const d = deployment("d", DeploymentStatus.SUPERSEDED, 1, {
			placements: [{ region: "us-va-1", desiredReplicas: 2 }],
		});
		expect(reportedReadyLabel(d, new Map())).toBeUndefined();
	});
});

test("placementReadyLabel counts one region's ready replicas of this deployment", () => {
	const replica = (deploymentId: string, ready: boolean) => ({ deploymentId, ready });
	const west = {
		region: "us-ca-1",
		desiredReplicas: 3,
		replicas: [replica("d", true), replica("d", false), replica("old", true)],
	};
	const d = deployment("d", DeploymentStatus.DEPLOYING, 1, { placements: [west] });
	const [placement] = d.placements;
	if (placement === undefined) throw new Error("no placement");
	expect(placementReadyLabel(d, placement)).toBe("1/3");
});

describe("canceledNote", () => {
	test("names who canceled the deployment", () => {
		const d = deployment("d", DeploymentStatus.CANCELED, 1, { canceledByName: "Nikhil" });
		expect(canceledNote(d)).toBe("Canceled by Nikhil");
	});
	test("names the deployment that replaced it", () => {
		const newer = "0196a5c0-0000-7000-8000-00000000beef";
		const d = deployment("d", DeploymentStatus.CANCELED, 1, {
			placements: [{ region: "us-va-1", supersededBy: newer }],
		});
		expect(canceledNote(d)).toBe(`Superseded by ${formatShortId(newer)}`);
	});
	test("says nothing for a deployment canceled with its service", () => {
		expect(canceledNote(deployment("d", DeploymentStatus.CANCELED, 1))).toBeUndefined();
	});
});

describe("cancelable", () => {
	test("a rollout with a region it never ran in can be canceled", () => {
		const d = deployment("d", DeploymentStatus.DEPLOYING, 1, {}, { status: PlacementStatus.DEPLOYING });
		expect(cancelable(d)).toBe(true);
	});
	test("a restart of a deployment that already ran everywhere cannot", () => {
		const d = deployment(
			"d",
			DeploymentStatus.DEPLOYING,
			1,
			{},
			{ status: PlacementStatus.DEPLOYING, startedAt: timestampFromMs(NOW - MIN) },
		);
		expect(cancelable(d)).toBe(false);
	});
	test("a deployment that runs but was never ready can", () => {
		const d = deployment("d", DeploymentStatus.DEGRADED, 1, {}, { status: PlacementStatus.DEGRADED });
		expect(cancelable(d)).toBe(true);
	});
	test("a finished deployment cannot", () => {
		const started = timestampFromMs(NOW - MIN);
		const d = deployment(
			"d",
			DeploymentStatus.RUNNING,
			1,
			{ startedAt: started },
			{ status: PlacementStatus.RUNNING, startedAt: started },
		);
		expect(cancelable(d)).toBe(false);
	});
});
