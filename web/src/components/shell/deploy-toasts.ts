import { DeploymentStatus, type Deployment } from "@gen/loco/deployment/v1/deployment_pb";

import { deploymentActor, deploymentImage, deploymentProblem, isInFlight, shortSha } from "@/lib/deployment-utils";
import { imageTag } from "@/lib/image";
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
	if (problem === undefined)
		return { state: "live", toast: { tone: "live", title: `${name} is live on ${deployRef(d)}`, description: "" } };
	const reason = (PROBLEM_REASONS[problem.reason] ?? problem.reason).toLowerCase();
	return { state: `warning:${problem.reason}`, toast: { tone: "warning", title: `${name}: ${reason}`, description } };
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
		const name = names.get(d.resourceId);
		const { state, toast } = standing(d, name ?? "");
		const known = seen.get(d.id);
		const quiet = known === undefined && (maybeTsMs(d.createdAt) ?? 0) < quietBeforeMs;
		const own = state === "deploying" && d.actorId === viewerId;
		if (toast !== undefined && !quiet && !own && name !== undefined && known !== state)
			toasts.push({ ...toast, deploymentId: d.id, resourceId: d.resourceId });
		next.set(d.id, state);
	}
	return { seen: next, toasts };
}
