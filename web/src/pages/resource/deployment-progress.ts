import type { Timestamp } from "@bufbuild/protobuf/wkt";
import {
	DeploymentStatus,
	type Activity,
	type Deployment,
	type DeploymentPlacement,
	type Replica,
} from "@gen/loco/deployment/v1/deployment_pb";

import type { BadgeTone } from "@/components/design/Badge";
import { currentReplica, readyIn } from "@/lib/activity-deploys";
import { deploymentProblem, isInFlight } from "@/lib/deployment-utils";
import { maybeTsMs, tsMs } from "@/lib/time";

import { lastGoodDeployment, type RegionView } from "./model";

export function progressDeployments(regions: readonly RegionView[]) {
	return regions.flatMap((region) => {
		const history = [
			...new Map(
				[...region.history, ...(region.current === undefined ? [] : [region.current])].map((d) => [d.id, d]),
			).values(),
		].toSorted((a, b) => tsMs(b.createdAt) - tsMs(a.createdAt));
		const deployment = history[0];
		if (deployment === undefined) return [];
		return [{ region, deployment, previous: lastGoodDeployment({ ...region, current: deployment, history }) }];
	});
}

export function deploymentElapsedMs(
	deployment: Deployment,
	nowMs: number,
	restart?: { startMs: number | undefined; endMs: number | undefined },
): number | undefined {
	const start = restart === undefined ? maybeTsMs(deployment.createdAt) : restart.startMs;
	if (start === undefined) return undefined;
	const finished =
		restart === undefined ? (maybeTsMs(deployment.completedAt) ?? maybeTsMs(deployment.startedAt)) : restart.endMs;
	const end = isInFlight(deployment.status) ? nowMs : (finished ?? nowMs);
	return Math.max(0, end - start);
}

/** The current revision's replicas, and those of the newest revision before it. */
export function groupReplicas(replicas: readonly Replica[], revision: bigint, deploymentId: string) {
	const others = replicas.filter((replica) => !currentReplica(replica, revision, deploymentId));
	const previous = others.reduce((newest, r) => (r.placementRevision > newest ? r.placementRevision : newest), -1n);
	return [
		{ label: "New version", replicas: replicas.filter((replica) => currentReplica(replica, revision, deploymentId)) },
		{ label: "Previous version", replicas: others.filter((replica) => replica.placementRevision === previous) },
	];
}

export function servingDeployments(replicas: readonly Replica[]) {
	const counts = new Map<string, number>();
	for (const replica of replicas) {
		if (replica.ready && replica.deploymentId !== "")
			counts.set(replica.deploymentId, (counts.get(replica.deploymentId) ?? 0) + 1);
	}
	return [...counts].map(([id, readyReplicas]) => ({ id, readyReplicas }));
}

export function replicaState(state: string): { label: string; tone: BadgeTone } {
	switch (state) {
		case "Scheduling":
			return { label: "Waiting for a node", tone: "muted" };
		case "Pulling":
			return { label: "Pulling image", tone: "info" };
		case "Starting":
			return { label: state, tone: "info" };
		case "Serving":
			return { label: state, tone: "ok" };
		case "Failing":
		case "Killed":
			return { label: state, tone: "bad" };
		case "Stopping":
			return { label: state, tone: "warn" };
		case "Stopped":
			return { label: state, tone: "muted" };
		default:
			return { label: "Unknown", tone: "muted" };
	}
}

export function stoppingCountdown(replica: Replica, nowMs: number) {
	const deadline = maybeTsMs(replica.stateDeadline);
	if (replica.state !== "Stopping" || deadline === undefined) return undefined;
	const since = maybeTsMs(replica.stateSince);
	const duration = since === undefined ? undefined : Math.max(0, deadline - since);
	const remaining = Math.max(0, Math.min(deadline - nowMs, duration ?? Infinity));
	return {
		seconds: Math.ceil(remaining / 1000),
		percent: duration === undefined ? undefined : duration === 0 ? 0 : (remaining / duration) * 100,
	};
}

/** How long after the rollout started an activity entry happened, as "+42s", "+1m 05s" or "+1h 02m". */
export function activityTime(at: Timestamp | undefined, startMs: number | undefined): string {
	const atMs = maybeTsMs(at);
	if (atMs === undefined || startMs === undefined) return "—";
	const total = Math.max(0, Math.floor((atMs - startMs) / 1000));
	const h = Math.floor(total / 3600);
	const m = Math.floor((total % 3600) / 60);
	const s = total % 60;
	if (h > 0) return `+${h.toString()}h ${m.toString().padStart(2, "0")}m`;
	if (m > 0) return `+${m.toString()}m ${s.toString().padStart(2, "0")}s`;
	return `+${s.toString()}s`;
}

/** A region's activity for its newest revision; a restart's feed leaves out earlier revisions and the build. */
export function rolloutActivity(activity: readonly Activity[], region: string, revision: bigint) {
	const regional = activity.filter((entry) => entry.region === region);
	const current = regional.reduce((newest, e) => (e.revision > newest ? e.revision : newest), revision);
	const restart = regional.some((entry) => entry.revision < current);
	const entries = activity
		.filter((entry) => (entry.region === "" ? !restart : entry.region === region && entry.revision === current))
		.toSorted((a, b) => (maybeTsMs(a.at) ?? 0) - (maybeTsMs(b.at) ?? 0) || Number(a.seq - b.seq));
	return {
		entries,
		restart: restart ? { startMs: maybeTsMs(entries[0]?.at), endMs: maybeTsMs(entries.at(-1)?.at) } : undefined,
	};
}

export function problemLogsHref(logsHref: string | undefined, deployment: Deployment): string | undefined {
	const query = deploymentProblem(deployment)?.logsQuery;
	if (logsHref === undefined || query === undefined || query === "") return undefined;
	const [path, search] = logsHref.split("?");
	const params = new URLSearchParams(search);
	params.set("q", query);
	return `${path}?${params}`;
}

/** How long after a deployment finishes its card stays on the resource page. */
export const CARD_LINGER_MS = 3 * 60_000;

export function showsCard(deployment: Deployment, nowMs: number): boolean {
	if (isInFlight(deployment.status)) return true;
	const done = maybeTsMs(deployment.completedAt) ?? maybeTsMs(deployment.updatedAt);
	return done !== undefined && nowMs - done <= CARD_LINGER_MS;
}

/** A duration as a running clock: "0:49", "12:05", "1:02:09". */
export function clockLabel(ms: number): string {
	const total = Math.max(0, Math.floor(ms / 1000));
	const h = Math.floor(total / 3600);
	const m = Math.floor((total % 3600) / 60);
	const s = (total % 60).toString().padStart(2, "0");
	return h > 0 ? `${h.toString()}:${m.toString().padStart(2, "0")}:${s}` : `${m.toString()}:${s}`;
}

export type StepState = "pending" | "active" | "done" | "failed" | "stopped";

export interface RolloutStep {
	key: "build" | "deploy";
	label: string;
	state: StepState;
}

export function rolloutSteps(deployment: Deployment, built: boolean): RolloutStep[] {
	const buildFailed = deploymentProblem(deployment)?.reason === "build_failed";
	const [build, deploy]: [StepState, StepState] = (() => {
		switch (deployment.status) {
			case DeploymentStatus.BUILDING:
				return ["active", "pending"];
			case DeploymentStatus.DEPLOYING:
				return ["done", "active"];
			case DeploymentStatus.FAILED:
			case DeploymentStatus.CRASHED:
				return buildFailed ? ["failed", "pending"] : ["done", "failed"];
			case DeploymentStatus.CANCELED:
				return built && deployment.startedAt === undefined ? ["stopped", "pending"] : ["done", "stopped"];
			case DeploymentStatus.RUNNING:
			case DeploymentStatus.DEGRADED:
			case DeploymentStatus.SUPERSEDED:
				return ["done", "done"];
			case DeploymentStatus.UNSPECIFIED:
				return ["pending", "pending"];
		}
	})();
	return [
		...(built ? [{ key: "build" as const, label: "Build image", state: build }] : []),
		{ key: "deploy", label: "Deploy replicas", state: deploy },
	];
}

export function readyLabel(deployment: Deployment, placement: DeploymentPlacement | undefined): string {
	const ready = placement === undefined ? 0 : readyIn(deployment, placement);
	return `${ready.toString()} of ${(placement?.desiredReplicas ?? 0).toString()} ready`;
}
