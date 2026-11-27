import { DeploymentStatus, type Deployment } from "@gen/loco/deployment/v1/deployment_pb";
import type { Event } from "@gen/loco/event/v1/event_pb";
import type { ReactNode } from "react";

import { InlineCode, withCode } from "@/components/design/InlineCode";

import {
	deploymentActor,
	deploymentHeadline,
	deploymentProblem,
	deploymentRef,
	isInFlight,
} from "@/lib/deployment-utils";
import { runtimeEvent } from "@/lib/runtime-events";
import { maybeTsMs } from "@/lib/time";
import { PROBLEM_REASONS } from "@/pages/resource/deployment-progress";

export interface DeployToast {
	deploymentId: string;
	resourceId: string;
	tone: "deploying" | "warning" | "failed" | "live";
	title: ReactNode;
	description: string;
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
						title: withCode`${name} is deploying`,
						description: `${deploymentActor(d)}, ${deploymentHeadline(d)}`,
					},
				}
			: {
					state: `failing:${problem.reason}`,
					toast: { tone: "warning", title: withCode`${name} deploy is failing`, description },
				};
	if (d.status === DeploymentStatus.FAILED || d.status === DeploymentStatus.CRASHED)
		return { state: "failed", toast: { tone: "failed", title: withCode`${name} deploy failed`, description: "" } };
	if (d.status !== DeploymentStatus.RUNNING && d.status !== DeploymentStatus.DEGRADED) return { state: "done" };
	// Problems on a live deployment toast from their resource.degraded event, see degradedToast.
	return {
		state: "live",
		toast: { tone: "live", title: withCode`${name} is live on ${deploymentRef(d)}`, description: "" },
	};
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
		title:
			short === undefined ? (
				withCode`${name} has a problem`
			) : (
				<>
					<InlineCode>{name}</InlineCode>: {short.toLowerCase()}
				</>
			),
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
