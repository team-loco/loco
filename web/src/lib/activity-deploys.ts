import { DeploymentStatus, type Deployment } from "@gen/loco/deployment/v1/deployment_pb";
import { ResourceStatus } from "@gen/loco/resource/v1/resource_pb";

import type { Glyph } from "@/components/design/StatusGlyph";
import type { Alert } from "@/lib/alerts";
import { getServiceSpec, isInFlight } from "@/lib/deployment-utils";
import { maybeTsMs } from "@/lib/time";

export const RECENT_MS = 30 * 60_000;
const MAX_CARDS = 6;

export type DeployState = "run" | "failed" | "live" | "canceled";
export type StepState = "pending" | "active" | "retry" | "done" | "skipped";

const STATE_ORDER: Record<DeployState, number> = { run: 0, failed: 1, live: 2, canceled: 3 };

export function deployState(status: DeploymentStatus): DeployState {
	switch (status) {
		case DeploymentStatus.BUILDING:
		case DeploymentStatus.DEPLOYING:
			return "run";
		case DeploymentStatus.FAILED:
			return "failed";
		case DeploymentStatus.CANCELED:
			return "canceled";
		case DeploymentStatus.RUNNING:
		case DeploymentStatus.SUPERSEDED:
		case DeploymentStatus.UNSPECIFIED:
			return "live";
	}
}

/** The deployments the Activity drawer shows: every one in flight and the rest from the last 30 minutes. */
export function activityDeployments(deployments: readonly Deployment[], nowMs: number): Deployment[] {
	const created = (d: Deployment) => maybeTsMs(d.createdAt) ?? 0;
	return deployments
		.filter((d) => isInFlight(d.status) || nowMs - created(d) < RECENT_MS)
		.toSorted(
			(a, b) => STATE_ORDER[deployState(a.status)] - STATE_ORDER[deployState(b.status)] || created(b) - created(a),
		)
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

export function readyLabel(d: Deployment): string {
	const ready = d.replicas.filter((r) => r.ready && (r.deploymentId === "" || r.deploymentId === d.id)).length;
	return `${ready.toString()} of ${d.desiredReplicas.toString()} ready`;
}

export function clockLabel(ms: number): string {
	const seconds = Math.max(0, Math.floor(ms / 1000));
	return `${Math.floor(seconds / 60).toString()}:${(seconds % 60).toString().padStart(2, "0")}`;
}

export function finishedLabel(d: Deployment, nowMs: number): string {
	const created = maybeTsMs(d.createdAt) ?? nowMs;
	const end = maybeTsMs(d.completedAt) ?? maybeTsMs(d.startedAt) ?? created;
	const mins = Math.floor((nowMs - end) / 60_000);
	return `took ${clockLabel(end - created)} · ${mins < 1 ? "just now" : `${mins.toString()}m ago`}`;
}

/** Drops a rollout alert when a deploy card already shows that rollout. */
export function withoutRunningDeploys(alerts: readonly Alert[], deployments: readonly Deployment[]): Alert[] {
	const running = new Set(deployments.filter((d) => isInFlight(d.status)).map((d) => `${d.resourceId}/${d.region}`));
	return alerts.filter((a) => a.status !== ResourceStatus.DEPLOYING || !running.has(a.key));
}
