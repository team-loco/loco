import { DeploymentStatus, type Deployment } from "@gen/loco/deployment/v1/deployment_pb";
import type { Event } from "@gen/loco/event/v1/event_pb";

import { deploymentActor, deploymentImage, deploymentProblem, isInFlight, shortSha } from "@/lib/deployment-utils";
import { imageTag } from "@/lib/image";
import { runtimeEvent } from "@/lib/runtime-events";
import { maybeTsMs } from "@/lib/time";
import { PROBLEM_REASONS } from "@/pages/resource/deployment-progress";

export interface DeployToast {
	deploymentId: string;
	resourceId: string;
	tone: "deploying" | "warning" | "failed" | "live";
	title: string;
	description: string;
}

/** What the deployment runs: its commit, or its image tag. */
function deployRef(d: Deployment): string {
	const sha = d.commit?.sha ?? "";
	return sha === "" ? imageTag(deploymentImage(d)) : shortSha(sha);
}

type Shown = Pick<DeployToast, "tone" | "title" | "description">;

/** Where a deployment stands, as far as toasts tell, and the toast that says so; a change of `state` is a toast. */
function standing(d: Deployment, name: string): { state: string; toast?: Shown } {
	const problem = deploymentProblem(d);
	const description = problem?.message ?? "";
	if (isInFlight(d.status))
		return problem === undefined
			? {
					state: "deploying",
					toast: {
						tone: "deploying",
						title: `${name} is deploying`,
						description: `${deploymentActor(d)}, ${deployRef(d)}`,
					},
				}
			: {
					state: `failing:${problem.reason}`,
					toast: { tone: "warning", title: `${name} deploy is failing`, description },
				};
	if (d.status === DeploymentStatus.FAILED || d.status === DeploymentStatus.CRASHED)
		return { state: "failed", toast: { tone: "failed", title: `${name} deploy failed`, description } };
	if (d.status !== DeploymentStatus.RUNNING && d.status !== DeploymentStatus.DEGRADED) return { state: "done" };
	// Problems on a live deployment toast from their resource.degraded event, see degradedToast.
	return { state: "live", toast: { tone: "live", title: `${name} is live on ${deployRef(d)}`, description: "" } };
}

/** The warning toast for a resource.degraded event, such as "api: out of memory"; null for other events or unknown resources. */
export function degradedToast(event: Event, names: ReadonlyMap<string, string>): DeployToast | null {
	const name = names.get(event.resourceId);
	if (event.type !== "resource.degraded" || name === undefined) return null;
	const { reason, message } = runtimeEvent(event);
	const short = PROBLEM_REASONS[reason];
	return {
		deploymentId: event.deploymentId || event.id,
		resourceId: event.resourceId,
		tone: "warning",
		title: short === undefined ? `${name} has a problem` : `${name}: ${short.toLowerCase()}`,
		description: message,
	};
}

/**
 * The toasts for deployments whose state changed since `seen`, and the states to remember.
 * A deployment not seen yet whose state last changed before `quietBeforeMs` is remembered without a toast.
 * A deployment of a resource without a name yet is left unseen, so it toasts once the name loads.
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
		const name = names.get(d.resourceId);
		if (name === undefined) continue;
		const { state, toast } = standing(d, name);
		const known = seen.get(d.id);
		const quiet = known === undefined && (maybeTsMs(d.updatedAt) ?? 0) < quietBeforeMs;
		const own = state === "deploying" && d.actorId === viewerId;
		if (toast !== undefined && !quiet && !own && known !== state)
			toasts.push({ ...toast, deploymentId: d.id, resourceId: d.resourceId });
		next.set(d.id, state);
	}
	return { seen: next, toasts };
}
