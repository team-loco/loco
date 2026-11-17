import { create } from "@bufbuild/protobuf";
import { timestampFromMs } from "@bufbuild/protobuf/wkt";
import {
	ActivitySchema,
	DeploymentSchema,
	DeploymentStatus,
	ReplicaSchema,
} from "@gen/loco/deployment/v1/deployment_pb";
import { expect, test } from "bun:test";

import {
	activityRows,
	deploymentElapsedMs,
	deploymentStatusLabel,
	groupReplicas,
	problemLogsHref,
	progressDeployments,
	replicaState,
	stoppingCountdown,
} from "./deployment-progress";
import type { RegionView } from "./model";

const previous = create(DeploymentSchema, {
	id: "previous",
	region: "east",
	status: DeploymentStatus.RUNNING,
	isActive: true,
	createdAt: timestampFromMs(1000),
	spec: { spec: { case: "service", value: { build: { type: "image", image: "example.test/app:v1" } } } },
});
const pending = create(DeploymentSchema, {
	id: "pending",
	region: "east",
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
		region: "west",
		createdAt: timestampFromMs(4000),
	});
	expect(
		progressDeployments([region, { name: "west", history: [other], current: other, status: undefined }]).map(
			(p) => p.deployment.id,
		),
	).toEqual(["pending", "other"]);
});

test("replicas group by placement revision, retaining stopped replicas", () => {
	const old = create(ReplicaSchema, { name: "old", placementRevision: 1n, state: "Stopped" });
	const next = create(ReplicaSchema, { name: "next", placementRevision: 2n });
	expect(groupReplicas([old, next], 2n)).toEqual([
		{ label: "New version", replicas: [next] },
		{ label: "Previous version", replicas: [old] },
	]);
	expect(groupReplicas([], 0n).map((g) => g.replicas)).toEqual([[], []]);
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

test("activity keeps sequence order and formats time from deployment start without mutating input", () => {
	const first = create(ActivitySchema, {
		seq: 1n,
		at: timestampFromMs(43000),
		severity: "ok",
		replicaName: "web-1",
		message: "Serving",
	});
	const second = create(ActivitySchema, {
		seq: 2n,
		at: timestampFromMs(3662000),
		severity: "error",
		message: "Exited",
	});
	const input = [second, first];
	expect(activityRows(input, timestampFromMs(1000))).toEqual([
		{ key: "1", time: "+0:42", severity: "ok", replicaName: "web-1", message: "Serving" },
		{ key: "2", time: "+61:01", severity: "error", replicaName: "", message: "Exited" },
	]);
	expect(input).toEqual([second, first]);
	expect(activityRows([create(ActivitySchema)], undefined)[0]?.time).toBe("—");
});

test("headers name each deployment status, including a running problem", () => {
	expect(
		[
			DeploymentStatus.BUILDING,
			DeploymentStatus.DEPLOYING,
			DeploymentStatus.RUNNING,
			DeploymentStatus.FAILED,
			DeploymentStatus.CANCELED,
			DeploymentStatus.SUPERSEDED,
		].map((status) => deploymentStatusLabel({ ...pending, status })),
	).toEqual(["Building", "Deploying", "Running", "Failed", "Canceled", "Superseded"]);
	expect(
		deploymentStatusLabel(
			create(DeploymentSchema, { status: DeploymentStatus.RUNNING, problem: { message: "Unhealthy" } }),
		),
	).toBe("Running · problem");
});

test("problem log links preserve scope and target replica or failed build", () => {
	const deployment = create(DeploymentSchema, { problem: { replicaName: "web-1" } });
	expect(problemLogsHref("/logs?env=prod&r=web", deployment)).toBe("/logs?env=prod&r=web&q=%40replica%3Aweb-1");
	const build = create(DeploymentSchema, {
		problem: { reason: "build_failed" },
		spec: { spec: { case: "service", value: { build: { buildId: "build-1" } } } },
	});
	expect(problemLogsHref("/logs?env=prod", build)).toBe("/logs?env=prod&q=%40build%3Abuild-1");
	expect(problemLogsHref(undefined, deployment)).toBeUndefined();
	expect(problemLogsHref("/logs", pending)).toBeUndefined();
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
});
