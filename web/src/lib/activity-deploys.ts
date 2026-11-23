import type { Timestamp } from "@bufbuild/protobuf/wkt";
import {
	DeploymentStatus,
	PlacementStatus,
	type Deployment,
	type DeploymentPlacement,
	type Replica,
} from "@gen/loco/deployment/v1/deployment_pb";
import { ResourceStatus, type Resource } from "@gen/loco/resource/v1/resource_pb";

import type { Glyph } from "@/components/design/StatusGlyph";
import type { Alert } from "@/lib/alerts";
import { deploymentProblem, desiredReplicas, getServiceSpec, isInFlight, placementPhase } from "@/lib/deployment-utils";
import { environmentStatus } from "@/lib/resource-status";
import { agoLabel, maybeTsMs, readyMs, tsMs } from "@/lib/time";
import { formatShortId } from "@/lib/utils";

export const RECENT_MS = 30 * 60_000;
export const ACTIVITY_PAGE = 6;

export type StepState = "pending" | "active" | "retry" | "done" | "skipped";

const ORDER: Record<DeploymentStatus, number> = {
	[DeploymentStatus.BUILDING]: 0,
	[DeploymentStatus.DEPLOYING]: 0,
	[DeploymentStatus.FAILED]: 1,
	[DeploymentStatus.CRASHED]: 1,
	[DeploymentStatus.RUNNING]: 2,
	[DeploymentStatus.DEGRADED]: 2,
	[DeploymentStatus.SUPERSEDED]: 2,
	[DeploymentStatus.UNSPECIFIED]: 2,
	[DeploymentStatus.CANCELED]: 3,
};

/** The deployments the Activity drawer lists: every one in flight and the rest from the last 30 minutes. */
export function activityDeployments(deployments: readonly Deployment[], nowMs: number): Deployment[] {
	const created = (d: Deployment) => maybeTsMs(d.createdAt) ?? 0;
	return deployments
		.filter((d) => isInFlight(d.status) || nowMs - created(d) < RECENT_MS)
		.toSorted((a, b) => ORDER[a.status] - ORDER[b.status] || created(b) - created(a));
}

export type ActivityLevel = "all" | "warn";

/** Whether an event is a warning or an error, the events the Warnings level keeps. */
function isWarningSeverity(severity: string): boolean {
	return severity === "warning" || severity === "error";
}

/** The events the drawer lists at a level, newest first. */
export function activityEvents<T extends { severity: string; timestamp: Timestamp | undefined }>(
	events: readonly T[],
	level: ActivityLevel,
): T[] {
	return events
		.filter((e) => level === "all" || isWarningSeverity(e.severity))
		.toSorted((a, b) => tsMs(b.timestamp) - tsMs(a.timestamp));
}

export function deployPhase(d: Deployment): { label: string; glyph: Glyph } {
	const problem = deploymentProblem(d) !== undefined;
	switch (d.status) {
		case DeploymentStatus.BUILDING:
			return problem ? { label: "Failing, retrying", glyph: "warn" } : { label: "Building", glyph: "spin" };
		case DeploymentStatus.DEPLOYING:
			return problem ? { label: "Failing, retrying", glyph: "warn" } : { label: "Deploying", glyph: "spin" };
		case DeploymentStatus.RUNNING:
		case DeploymentStatus.SUPERSEDED:
			return problem ? { label: "Running", glyph: "warn" } : { label: "Deployed", glyph: "ok" };
		case DeploymentStatus.DEGRADED:
			return { label: "Running", glyph: "warn" };
		case DeploymentStatus.FAILED:
		case DeploymentStatus.CRASHED:
			return { label: "Failed", glyph: "err" };
		case DeploymentStatus.CANCELED:
			return { label: "Canceled", glyph: "off" };
		case DeploymentStatus.UNSPECIFIED:
			return { label: "Queued", glyph: "draft" };
	}
}

/** The build and deploy steps of a deployment in flight, and which one it is on. */
export function deploySteps(d: Deployment): { build: StepState; deploy: StepState; current: "build" | "deploy" } {
	const active: StepState = deploymentProblem(d) === undefined ? "active" : "retry";
	if (d.status === DeploymentStatus.BUILDING) return { build: active, deploy: "pending", current: "build" };
	const built = getServiceSpec(d)?.build?.buildId ? "done" : "skipped";
	return { build: built, deploy: active, current: "deploy" };
}

/** Whether a replica runs the placement's current revision of the deployment. */
export function currentReplica(replica: Replica, revision: bigint, deploymentId: string): boolean {
	return (
		replica.placementRevision === revision && (replica.deploymentId === "" || replica.deploymentId === deploymentId)
	);
}

/** The ready replicas of the placement's current revision of the deployment. */
export function readyIn(d: Deployment, p: DeploymentPlacement): number {
	return p.replicas.filter((r) => r.ready && currentReplica(r, p.currentRevision, d.id)).length;
}

function readyCount(d: Deployment): number {
	return d.placements.reduce((sum, p) => sum + readyIn(d, p), 0);
}

/** How many of a region's desired replicas of the deployment are ready, as "1/3". */
export function placementReadyLabel(d: Deployment, p: DeploymentPlacement): string {
	return `${readyIn(d, p).toString()}/${p.desiredReplicas.toString()}`;
}

export function clockLabel(ms: number): string {
	const seconds = Math.max(0, Math.floor(ms / 1000));
	return `${Math.floor(seconds / 60).toString()}:${(seconds % 60).toString().padStart(2, "0")}`;
}

/** A duration with units, such as "59s", "4m 49s" or "1h 2m". */
export function tookLabel(ms: number): string {
	const total = Math.max(0, Math.floor(ms / 1000));
	const hours = Math.floor(total / 3600);
	const minutes = Math.floor((total % 3600) / 60);
	const seconds = total % 60;
	if (hours > 0) return minutes > 0 ? `${hours.toString()}h ${minutes.toString()}m` : `${hours.toString()}h`;
	if (minutes > 0) return seconds > 0 ? `${minutes.toString()}m ${seconds.toString()}s` : `${minutes.toString()}m`;
	return `${seconds.toString()}s`;
}

export function finishedLabel(d: Deployment, nowMs: number): string {
	const created = maybeTsMs(d.createdAt) ?? nowMs;
	const end = readyMs(d) ?? maybeTsMs(d.completedAt) ?? created;
	return `took ${tookLabel(end - created)}, ${agoLabel(end, nowMs)}`;
}

/** The resource/region keys of the deployments in flight. */
export function runningDeployRegions(deployments: readonly Deployment[]): Set<string> {
	return new Set(
		deployments
			.filter((d) => isInFlight(d.status))
			.flatMap((d) => d.placements.map((p) => `${d.resourceId}/${p.region}`)),
	);
}

/** The resource/region keys whose live placement in the environment is in flight. */
export function rollingOutRegions(resources: readonly Resource[], environmentId: string): Set<string> {
	return new Set(
		resources.flatMap((resource) =>
			(environmentStatus(resource, environmentId)?.regions ?? [])
				.filter((r) => isInFlight(placementPhase(r.placementStatus)))
				.map((r) => `${resource.id}/${r.region}`),
		),
	);
}

/** Drops a rollout alert when a deploy card already shows that rollout. */
export function withoutRunningDeploys(alerts: readonly Alert[], running: ReadonlySet<string>): Alert[] {
	return alerts.filter((a) => a.status !== ResourceStatus.DEPLOYING || !running.has(a.key));
}

export function rolloutReadyLabel(d: Deployment, count = readyCount(d)): string {
	const label = `${count.toString()} of ${desiredReplicas(d).toString()} ready`;
	return d.placements.length <= 1 ? label : `${label} in ${d.placements.length.toString()} regions`;
}

/** The ready count the clusters report for a deployment's live regions; nothing when none reports on it. */
export function reportedReadyLabel(d: Deployment, ready: ReadonlyMap<string, number>): string | undefined {
	const count = ready.get(d.id);
	return count === undefined ? undefined : rolloutReadyLabel(d, count);
}

/** Who canceled a canceled deployment, or the deployment that replaced it. */
export function canceledNote(d: Deployment): string | undefined {
	if (d.canceledByName !== "") return `Canceled by ${d.canceledByName}`;
	const newer = d.placements.find((p) => p.supersededBy !== "")?.supersededBy;
	return newer === undefined ? undefined : `Superseded by ${formatShortId(newer)}`;
}

const UNSTARTED_PLACEMENTS: ReadonlySet<PlacementStatus> = new Set([
	PlacementStatus.PENDING,
	PlacementStatus.DEPLOYING,
	PlacementStatus.RUNNING,
	PlacementStatus.DEGRADED,
]);

/** Whether a cancel has a region to stop: one the deployment never deployed in that has not ended. The API's inFlight (api/service/deployment_cancel.go) owns this rule and its placement check. */
export function cancelable(d: Deployment): boolean {
	const serving = d.status === DeploymentStatus.RUNNING || d.status === DeploymentStatus.DEGRADED;
	return (
		(isInFlight(d.status) || (serving && d.startedAt === undefined)) &&
		d.placements.some((p) => p.startedAt === undefined && UNSTARTED_PLACEMENTS.has(p.status))
	);
}
