import { DeploymentStatus, type Deployment } from "@gen/loco/deployment/v1/deployment_pb";
import { ResourceStatus } from "@gen/loco/resource/v1/resource_pb";

import type { Glyph } from "@/components/design/StatusGlyph";
import type { Alert } from "@/lib/alerts";
import { getServiceSpec, isInFlight } from "@/lib/deployment-utils";
import { maybeTsMs, readyMs } from "@/lib/time";

export const RECENT_MS = 30 * 60_000;
const MAX_CARDS = 6;

export type StepState = "pending" | "active" | "retry" | "done" | "skipped";

const ORDER: Record<DeploymentStatus, number> = {
	[DeploymentStatus.BUILDING]: 0,
	[DeploymentStatus.DEPLOYING]: 0,
	[DeploymentStatus.FAILED]: 1,
	[DeploymentStatus.RUNNING]: 2,
	[DeploymentStatus.SUPERSEDED]: 2,
	[DeploymentStatus.UNSPECIFIED]: 2,
	[DeploymentStatus.CANCELED]: 3,
};

/** The deployments the Activity drawer shows: every one in flight and the rest from the last 30 minutes. */
export function activityDeployments(deployments: readonly Deployment[], nowMs: number): Deployment[] {
	const created = (d: Deployment) => maybeTsMs(d.createdAt) ?? 0;
	return deployments
		.filter((d) => isInFlight(d.status) || nowMs - created(d) < RECENT_MS)
		.toSorted((a, b) => ORDER[a.status] - ORDER[b.status] || created(b) - created(a))
		.slice(0, MAX_CARDS);
}

export function deployPhase(d: Deployment): { label: string; glyph: Glyph } {
	const problem = d.problem !== undefined;
	switch (d.status) {
		case DeploymentStatus.BUILDING:
			return problem ? { label: "Failing, retrying", glyph: "warn" } : { label: "Building", glyph: "spin" };
		case DeploymentStatus.DEPLOYING:
			return problem ? { label: "Failing, retrying", glyph: "warn" } : { label: "Deploying", glyph: "spin" };
		case DeploymentStatus.RUNNING:
		case DeploymentStatus.SUPERSEDED:
			return problem ? { label: "Running", glyph: "warn" } : { label: "Deployed", glyph: "ok" };
		case DeploymentStatus.FAILED:
			return { label: "Failed", glyph: "err" };
		case DeploymentStatus.CANCELED:
			return { label: "Canceled", glyph: "off" };
		case DeploymentStatus.UNSPECIFIED:
			return { label: "Queued", glyph: "draft" };
	}
}

/** The build and deploy steps of a deployment in flight, and which one it is on. */
export function deploySteps(d: Deployment): { build: StepState; deploy: StepState; current: "build" | "deploy" } {
	const active: StepState = d.problem === undefined ? "active" : "retry";
	if (d.status === DeploymentStatus.BUILDING) return { build: active, deploy: "pending", current: "build" };
	const built = getServiceSpec(d)?.build?.buildId ? "done" : "skipped";
	return { build: built, deploy: active, current: "deploy" };
}

function readyCount(d: Deployment): number {
	return d.replicas.filter((r) => r.ready && (r.deploymentId === "" || r.deploymentId === d.id)).length;
}

export function readyLabel(d: Deployment): string {
	return `${readyCount(d).toString()} of ${d.desiredReplicas.toString()} ready`;
}

export function clockLabel(ms: number): string {
	const seconds = Math.max(0, Math.floor(ms / 1000));
	return `${Math.floor(seconds / 60).toString()}:${(seconds % 60).toString().padStart(2, "0")}`;
}

export function finishedLabel(d: Deployment, nowMs: number): string {
	const created = maybeTsMs(d.createdAt) ?? nowMs;
	const end = readyMs(d) ?? maybeTsMs(d.completedAt) ?? created;
	const mins = Math.floor((nowMs - end) / 60_000);
	return `took ${clockLabel(end - created)} · ${mins < 1 ? "just now" : `${mins.toString()}m ago`}`;
}

/** Drops a rollout alert when a deploy card already shows that rollout. */
export function withoutRunningDeploys(alerts: readonly Alert[], deployments: readonly Deployment[]): Alert[] {
	const running = new Set(deployments.filter((d) => isInFlight(d.status)).map((d) => `${d.resourceId}/${d.region}`));
	return alerts.filter((a) => a.status !== ResourceStatus.DEPLOYING || !running.has(a.key));
}

/** Deployments grouped by the rollout that created them, in order of each rollout's first deployment. */
export function rolloutGroups(deployments: readonly Deployment[]): Deployment[][] {
	return [...Map.groupBy(deployments, (d) => d.rolloutId).values()];
}

export function rolloutReadyLabel(deployments: readonly Deployment[]): string {
	if (deployments.length === 1 && deployments[0] !== undefined) return readyLabel(deployments[0]);
	const ready = deployments.reduce((sum, d) => sum + readyCount(d), 0);
	const desired = deployments.reduce((sum, d) => sum + d.desiredReplicas, 0);
	return `${ready.toString()} of ${desired.toString()} ready in ${deployments.length.toString()} regions`;
}
