import { create } from "@bufbuild/protobuf";
import { timestampFromMs } from "@bufbuild/protobuf/wkt";
import {
	ActivitySchema,
	DeploymentPlacementSchema,
	DeploymentSchema,
	DeploymentStatus,
	PlacementStatus,
	ReplicaSchema,
} from "@gen/loco/deployment/v1/deployment_pb";
import { RegionStatusSchema } from "@gen/loco/resource/v1/resource_pb";
import { expect, test } from "bun:test";

import {
	activityTime,
	CARD_LINGER_MS,
	clockLabel,
	deploymentElapsedMs,
	groupReplicas,
	problemLogsHref,
	progressDeployments,
	readyLabel,
	replicaState,
	rolloutActivity,
	rolloutSteps,
	showsCard,
	stoppingCountdown,
	servingBehindStop,
	servingDeployments,
} from "./deployment-progress";
import type { RegionView } from "./model";

const previous = create(DeploymentSchema, {
	id: "previous",
	placements: [{ region: "east", live: true }],
	status: DeploymentStatus.RUNNING,
	createdAt: timestampFromMs(1000),
	spec: { spec: { case: "service", value: { build: { type: "image", image: "example.test/app:v1" } } } },
});
const pending = create(DeploymentSchema, {
	id: "pending",
	placements: [{ region: "east" }],
	status: DeploymentStatus.BUILDING,
	createdAt: timestampFromMs(3000),
});
const region: RegionView = { name: "east", history: [pending, previous], current: previous, status: undefined };

test("a new pending deployment appears while the region still runs its previous release", () => {
	const [progress] = progressDeployments([region]);
	expect(progress?.deployment.id).toBe("pending");
	expect(progress?.previous?.id).toBe("previous");
});

test("a failed newest deployment offers the previous running release", () => {
	const failed = create(DeploymentSchema, { ...pending, status: DeploymentStatus.FAILED });
	expect(progressDeployments([{ ...region, history: [failed, previous] }])[0]?.previous?.id).toBe("previous");
	const running = create(DeploymentSchema, { ...pending, status: DeploymentStatus.RUNNING });
	expect(progressDeployments([{ ...region, history: [running, previous] }])[0]?.deployment).toBe(running);
});

test("progress does not use a sibling region's deployments", () => {
	const other = create(DeploymentSchema, {
		...previous,
		id: "other",
		placements: [create(DeploymentPlacementSchema, { region: "west", live: true })],
		createdAt: timestampFromMs(4000),
	});
	expect(
		progressDeployments([region, { name: "west", history: [other], current: other, status: undefined }]).map(
			(p) => p.deployment.id,
		),
	).toEqual(["pending", "other"]);
});

test("previous version holds only the newest earlier revision", () => {
	const older = create(ReplicaSchema, {
		name: "older",
		placementRevision: 1n,
		deploymentId: "first",
		state: "Stopped",
	});
	const old = create(ReplicaSchema, { name: "old", placementRevision: 2n, deploymentId: "next", ready: true });
	const next = create(ReplicaSchema, { name: "next", placementRevision: 3n, deploymentId: "next" });
	expect(groupReplicas([older, old, next], 3n, "next")).toEqual([
		{ label: "New version", replicas: [next] },
		{ label: "Previous version", replicas: [old] },
	]);
});

test("replicas group by placement revision, retaining stopped replicas", () => {
	const old = create(ReplicaSchema, { name: "old", placementRevision: 1n, state: "Stopped" });
	const next = create(ReplicaSchema, { name: "next", placementRevision: 2n });
	expect(groupReplicas([old, next], 2n, "next")).toEqual([
		{ label: "New version", replicas: [next] },
		{ label: "Previous version", replicas: [old] },
	]);
	expect(groupReplicas([], 0n, "next").map((g) => g.replicas)).toEqual([[], []]);
});

test("a retiring placement's revision one stays separate from the desired deployment", () => {
	const old = create(ReplicaSchema, { deploymentId: "old-deployment", placementRevision: 1n, ready: true });
	const next = create(ReplicaSchema, { deploymentId: "next-deployment", placementRevision: 1n, ready: true });
	expect(groupReplicas([old, next], 1n, "next-deployment")).toEqual([
		{ label: "New version", replicas: [next] },
		{ label: "Previous version", replicas: [old] },
	]);
});

test("replica states have readable labels and semantic tones", () => {
	expect(
		["Scheduling", "Pulling", "Starting", "Serving", "Failing", "Stopping", "Stopped", "Killed", "Unknown"].map(
			replicaState,
		),
	).toEqual([
		{ label: "Waiting for a node", tone: "muted" },
		{ label: "Pulling image", tone: "info" },
		{ label: "Starting", tone: "info" },
		{ label: "Serving", tone: "ok" },
		{ label: "Failing", tone: "bad" },
		{ label: "Stopping", tone: "warn" },
		{ label: "Stopped", tone: "muted" },
		{ label: "Killed", tone: "bad" },
		{ label: "Unknown", tone: "muted" },
	]);
	expect(replicaState("future")).toEqual({ label: "Unknown", tone: "muted" });
});

test("stopping countdown uses the reported interval and clamps clock skew and expiry", () => {
	const replica = create(ReplicaSchema, {
		state: "Stopping",
		stateSince: timestampFromMs(10000),
		stateDeadline: timestampFromMs(40000),
	});
	expect(stoppingCountdown(replica, 25000)).toEqual({ seconds: 15, percent: 50 });
	expect(stoppingCountdown(replica, 0)).toEqual({ seconds: 30, percent: 100 });
	expect(stoppingCountdown(replica, 50000)).toEqual({ seconds: 0, percent: 0 });
	expect(stoppingCountdown({ ...replica, stateSince: undefined }, 25000)).toEqual({ seconds: 15, percent: undefined });
	expect(stoppingCountdown({ ...replica, stateDeadline: timestampFromMs(10000) }, 25000)).toEqual({
		seconds: 0,
		percent: 0,
	});
	expect(stoppingCountdown({ ...replica, stateDeadline: undefined }, 25000)).toBeUndefined();
	expect(stoppingCountdown({ ...replica, state: "Stopped" }, 25000)).toBeUndefined();
});

test("activity time reads as a duration since the rollout started", () => {
	expect(activityTime(timestampFromMs(43000), 1000)).toBe("+42s");
	expect(activityTime(timestampFromMs(66000), 1000)).toBe("+1m 05s");
	expect(activityTime(timestampFromMs(3662000), 1000)).toBe("+1h 01m");
	expect(activityTime(undefined, 1000)).toBe("—");
	expect(activityTime(timestampFromMs(1000), undefined)).toBe("—");
});

test("a restart's feed shows only its own revision in the region", () => {
	const entry = (seq: number, name: string, revision: number, atMs: number) =>
		create(ActivitySchema, { seq: BigInt(seq), region: name, revision: BigInt(revision), at: timestampFromMs(atMs) });
	const build = entry(1, "", 0, 1000);
	const first = entry(2, "east", 2, 2000);
	const west = entry(3, "west", 2, 2500);
	const restarted = entry(4, "east", 3, 9000);
	const done = entry(5, "east", 3, 12000);
	expect(rolloutActivity([build, first, west], "east", 2n)).toEqual({ entries: [build, first], restart: undefined });
	expect(rolloutActivity([build, first, west, restarted, done], "east", 3n)).toEqual({
		entries: [restarted, done],
		restart: { startMs: 9000, endMs: 12000 },
	});
	expect(rolloutActivity([build, first, restarted], "east", 2n).entries).toEqual([restarted]);
	expect(rolloutActivity([build, west], "west", 3n)).toEqual({
		entries: [],
		restart: { startMs: undefined, endMs: undefined },
	});
});

test("problem log links carry the API's logs query and keep the page's scope", () => {
	const replica = create(DeploymentSchema, {
		placements: [{ problem: { replicaName: "web-1", logsQuery: "@replica:web-1" } }],
	});
	expect(problemLogsHref("/logs?env=prod&r=web", replica)).toBe("/logs?env=prod&r=web&q=%40replica%3Aweb-1");
	const build = create(DeploymentSchema, {
		placements: [{ problem: { reason: "build_failed", logsQuery: "@build:build-1" } }],
	});
	expect(problemLogsHref("/logs?env=prod", build)).toBe("/logs?env=prod&q=%40build%3Abuild-1");
	expect(problemLogsHref(undefined, replica)).toBeUndefined();
	expect(problemLogsHref("/logs", pending)).toBeUndefined();
	expect(
		problemLogsHref("/logs", create(DeploymentSchema, { placements: [{ problem: { message: "No logs query" } }] })),
	).toBeUndefined();
});

test("elapsed rollout time includes the build and stops when ready or finished", () => {
	const ready = create(DeploymentSchema, {
		...pending,
		status: DeploymentStatus.RUNNING,
		startedAt: timestampFromMs(13000),
	});
	expect(deploymentElapsedMs(pending, 20000)).toBe(17000);
	expect(deploymentElapsedMs(ready, 20000)).toBe(10000);
	expect(deploymentElapsedMs({ ...ready, status: DeploymentStatus.DEPLOYING }, 20000)).toBe(17000);
	expect(
		deploymentElapsedMs({ ...ready, status: DeploymentStatus.FAILED, completedAt: timestampFromMs(18000) }, 20000),
	).toBe(15000);
	expect(deploymentElapsedMs({ ...pending, createdAt: undefined }, 20000)).toBeUndefined();
	expect(deploymentElapsedMs(pending, 0)).toBe(0);
	expect(deploymentElapsedMs(ready, 20000, { startMs: 15000, endMs: 18000 })).toBe(3000);
	expect(
		deploymentElapsedMs({ ...ready, status: DeploymentStatus.DEPLOYING }, 20000, { startMs: 15000, endMs: 18000 }),
	).toBe(5000);
});

test("serving deployments use reported readiness and Pod identity across mixed revisions", () => {
	const replicas = [
		create(ReplicaSchema, { deploymentId: "old", ready: true, state: "Serving" }),
		create(ReplicaSchema, { deploymentId: "old", ready: true, state: "Failing" }),
		create(ReplicaSchema, { deploymentId: "next", ready: true, state: "Serving" }),
		create(ReplicaSchema, { deploymentId: "stopping", state: "Stopping" }),
		create(ReplicaSchema, { deploymentId: "failed", state: "Serving" }),
		create(ReplicaSchema, { ready: true, state: "Serving" }),
	];
	expect(servingDeployments(replicas)).toEqual([
		{ id: "old", readyReplicas: 2 },
		{ id: "next", readyReplicas: 1 },
	]);
	expect(servingDeployments([])).toEqual([]);
});

test("the card shows while a deployment runs and for a few minutes after it finishes", () => {
	const done = create(DeploymentSchema, { status: DeploymentStatus.RUNNING, completedAt: timestampFromMs(10_000) });
	expect(showsCard(pending, 10_000_000)).toBe(true);
	expect(showsCard(done, 10_000 + CARD_LINGER_MS)).toBe(true);
	expect(showsCard(done, 10_001 + CARD_LINGER_MS)).toBe(false);
});

test("elapsed time reads like a clock", () => {
	expect([clockLabel(49_000), clockLabel(725_000), clockLabel(3_729_000)]).toEqual(["0:49", "12:05", "1:02:09"]);
});

test("rollout steps follow the deployment phase", () => {
	const at = (status: DeploymentStatus, reason = "") =>
		rolloutSteps(
			create(DeploymentSchema, { status, ...(reason === "" ? {} : { placements: [{ problem: { reason } }] }) }),
			true,
		).map((s) => s.state);
	expect(at(DeploymentStatus.BUILDING)).toEqual(["active", "pending"]);
	expect(at(DeploymentStatus.DEPLOYING)).toEqual(["done", "active"]);
	expect(at(DeploymentStatus.RUNNING)).toEqual(["done", "done"]);
	expect(at(DeploymentStatus.FAILED, "build_failed")).toEqual(["failed", "pending"]);
	expect(at(DeploymentStatus.FAILED, "restarting")).toEqual(["done", "failed"]);
	expect(
		rolloutSteps(create(DeploymentSchema, { status: DeploymentStatus.DEPLOYING }), false).map((s) => s.key),
	).toEqual(["deploy"]);
});

test("the ready line counts the current revision's ready replicas against the desired count", () => {
	const r = (ready: boolean, placementRevision: bigint, deploymentId = "next") =>
		create(ReplicaSchema, { ready, placementRevision, deploymentId });
	const deployment = create(DeploymentSchema, { id: "next" });
	const placement = create(DeploymentPlacementSchema, {
		desiredReplicas: 2,
		currentRevision: 3n,
		replicas: [r(true, 3n), r(false, 3n), r(true, 2n), r(true, 3n, "old")],
	});
	expect(readyLabel(deployment, placement)).toBe("1 of 2 ready");
	expect(readyLabel(deployment, undefined)).toBe("0 of 0 ready");
});

test("a feed is ordered by event time, then arrival", () => {
	const entry = (seq: number, atMs: number) =>
		create(ActivitySchema, { seq: BigInt(seq), region: "east", revision: 2n, at: timestampFromMs(atMs) });
	const late = entry(1, 5000);
	const early = entry(2, 3000);
	const tie = entry(3, 5000);
	expect(rolloutActivity([late, early, tie], "east", 2n).entries).toEqual([early, late, tie]);
});

test("a finished rollout's feed ignores the placement revision", () => {
	const entry = create(ActivitySchema, { seq: 1n, region: "east", revision: 2n, at: timestampFromMs(1000) });
	expect(rolloutActivity([entry], "east", 0n).entries).toEqual([entry]);
});

test("a stopped live deployment names the older deployment its region still serves", () => {
	const failed = create(DeploymentSchema, { ...pending, status: DeploymentStatus.FAILED });
	const status = (placementStatus: PlacementStatus, ready: string[]) =>
		create(RegionStatusSchema, {
			region: "east",
			placementStatus,
			replicas: ready.map((deploymentId) => create(ReplicaSchema, { deploymentId, ready: true })),
		});
	const stopped = (s: ReturnType<typeof status>): RegionView => ({
		...region,
		current: failed,
		history: [failed, previous],
		status: s,
	});
	const found = servingBehindStop([stopped(status(PlacementStatus.FAILED, ["previous", "previous"]))]);
	expect([found?.stopped.id, found?.serving.id]).toEqual(["pending", "previous"]);
	expect(servingBehindStop([stopped(status(PlacementStatus.CRASHED, ["previous"]))])?.serving.id).toBe("previous");
	expect(servingBehindStop([stopped(status(PlacementStatus.FAILED, ["pending"]))])).toBeUndefined();
	expect(servingBehindStop([stopped(status(PlacementStatus.FAILED, []))])).toBeUndefined();
	expect(servingBehindStop([stopped(status(PlacementStatus.DEPLOYING, ["previous"]))])).toBeUndefined();
});
