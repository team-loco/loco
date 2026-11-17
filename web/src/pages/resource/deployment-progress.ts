import type { Build } from "@gen/loco/build/v1/build_pb";
import { BuildStatus } from "@gen/loco/build/v1/build_pb";
import { DeploymentStatus, type Deployment } from "@gen/loco/deployment/v1/deployment_pb";

import type { StepState } from "@/lib/deploy-progress";
import { isInFlight } from "@/lib/deployment-utils";
import { tsMs } from "@/lib/time";

import { buildStatusStyle, buildTookMs, formatBuildDuration } from "./builds";
import { lastGoodDeployment, type RegionView } from "./model";

export function progressDeployments(regions: readonly RegionView[]) {
	return regions.flatMap((region) => {
		const history = [
			...new Map(
				[...region.history, ...(region.current === undefined ? [] : [region.current])].map((d) => [d.id, d]),
			).values(),
		].toSorted((a, b) => tsMs(b.createdAt) - tsMs(a.createdAt));
		const deployment = history[0];
		if (
			deployment === undefined ||
			(!isInFlight(deployment.status) &&
				deployment.status !== DeploymentStatus.FAILED &&
				deployment.status !== DeploymentStatus.CANCELED)
		)
			return [];
		return [{ region, deployment, previous: lastGoodDeployment({ ...region, current: deployment, history }) }];
	});
}

export interface DeploymentStep {
	label: string;
	detail: string;
	state: StepState;
}

export function deploymentSteps(deployment: Deployment, build: Build | undefined): DeploymentStep[] {
	const { status } = deployment;
	const done = status === DeploymentStatus.RUNNING || status === DeploymentStatus.SUPERSEDED;
	const stopped = status === DeploymentStatus.FAILED || status === DeploymentStatus.CANCELED;
	const active = status === DeploymentStatus.BUILDING ? 0 : 1;
	const steps = [
		{ label: "Queued", detail: "" },
		{ label: "Deploying", detail: "" },
		{ label: "Running", detail: "" },
	].map((step, index): DeploymentStep => ({
		...step,
		state: done || index < active ? "done" : index > active ? "todo" : stopped ? "failed" : "active",
	}));
	if (build !== undefined) {
		const duration = buildTookMs(build);
		steps.unshift({
			label: "Build",
			detail: [
				build.gitBranch,
				duration === undefined ? undefined : formatBuildDuration(duration),
				buildStatusStyle(build.status).label,
			]
				.filter(Boolean)
				.join(" · "),
			state:
				build.status === BuildStatus.SUCCEEDED
					? "done"
					: build.status === BuildStatus.FAILED || build.status === BuildStatus.CANCELED
						? "failed"
						: "active",
		});
	}
	return steps;
}
