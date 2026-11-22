import { DeploymentStatus, type Deployment } from "@gen/loco/deployment/v1/deployment_pb";

import { deploymentActor, deploymentImage, deploymentProblem, isInFlight, shortSha } from "@/lib/deployment-utils";
import { imageTag } from "@/lib/image";
import { maybeTsMs } from "@/lib/time";
import { PROBLEM_REASONS } from "@/pages/resource/deployment-progress";

export type DeployToastTone = "deploying" | "warning" | "failed" | "live";

export interface DeployToast {
	deploymentId: string;
	resourceId: string;
	tone: DeployToastTone;
	title: string;
	description: string;
}

/** Where a deployment stands, as far as toasts tell: a change of state is a toast. */
function stateOf(d: Deployment): string {
	const reason = deploymentProblem(d)?.reason;
	if (isInFlight(d.status)) return reason === undefined ? "deploying" : `failing:${reason}`;
	switch (d.status) {
		case DeploymentStatus.FAILED:
		case DeploymentStatus.CRASHED:
			return "failed";
		case DeploymentStatus.RUNNING:
		case DeploymentStatus.DEGRADED:
			return reason === undefined ? "live" : `warning:${reason}`;
		case DeploymentStatus.BUILDING:
		case DeploymentStatus.DEPLOYING:
		case DeploymentStatus.CANCELED:
		case DeploymentStatus.SUPERSEDED:
		case DeploymentStatus.UNSPECIFIED:
			return "done";
	}
}

/** What the deployment runs: its commit, or its image tag. */
function deployRef(d: Deployment): string {
	const sha = d.commit?.sha ?? "";
	return sha === "" ? imageTag(deploymentImage(d)) : shortSha(sha);
}

function toastFor(d: Deployment, state: string, name: string): DeployToast | null {
	const problem = deploymentProblem(d);
	const base = { deploymentId: d.id, resourceId: d.resourceId, description: problem?.message ?? "" };
	if (state === "deploying")
		return {
			...base,
			tone: "deploying",
			title: `${name} is deploying`,
			description: `${deploymentActor(d)}, ${deployRef(d)}`,
		};
	if (state.startsWith("failing:")) return { ...base, tone: "warning", title: `${name} deploy is failing` };
	if (state === "failed") return { ...base, tone: "failed", title: `${name} deploy failed` };
	if (state.startsWith("warning:")) {
		const reason = problem?.reason ?? "";
		return { ...base, tone: "warning", title: `${name}: ${(PROBLEM_REASONS[reason] ?? reason).toLowerCase()}` };
	}
	if (state === "live") return { ...base, tone: "live", title: `${name} is live on ${deployRef(d)}`, description: "" };
	return null;
}

/**
 * The toasts for deployments whose state changed since `seen`, and the states to remember.
 * A deployment created before `quietBeforeMs` and not seen yet is remembered without a toast.
 * The viewer's own deployments get no "is deploying" toast, since their action already said so.
 */
export function deployToasts(
	seen: ReadonlyMap<string, string>,
	deployments: readonly Deployment[],
	names: ReadonlyMap<string, string>,
	viewerId: string,
	quietBeforeMs: number,
): { seen: Map<string, string>; toasts: DeployToast[] } {
	const next = new Map(seen);
	const toasts: DeployToast[] = [];
	for (const d of deployments) {
		const state = stateOf(d);
		const name = names.get(d.resourceId);
		const known = seen.get(d.id);
		const quiet = known === undefined && (maybeTsMs(d.createdAt) ?? 0) < quietBeforeMs;
		if (!quiet && name !== undefined && known !== state) {
			const own = state === "deploying" && d.actorId === viewerId;
			const toast = own ? null : toastFor(d, state, name);
			if (toast !== null) toasts.push(toast);
		}
		next.set(d.id, state);
	}
	return { seen: next, toasts };
}
