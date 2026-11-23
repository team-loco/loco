import {
	DeploymentActorType,
	DeploymentMethod,
	DeploymentStatus,
	DeploymentTrigger,
	PlacementStatus,
	type Deployment,
	type DeploymentPlacement,
	type DeploymentSpec,
	type Problem,
	type RegionDeploymentTarget,
	type ServiceDeploymentSpec,
} from "@gen/loco/deployment/v1/deployment_pb";

import { imageTag } from "@/lib/image";

export function getServiceSpec(deployment: Deployment): ServiceDeploymentSpec | undefined {
	if (!deployment.spec?.spec) {
		return undefined;
	}

	if (deployment.spec.spec.case === "service") {
		return deployment.spec.spec.value;
	}

	return undefined;
}

/** The sizing of a region of a service spec; without a region, the first region's. */
export function regionTarget(
	spec: ServiceDeploymentSpec | undefined,
	region?: string,
): RegionDeploymentTarget | undefined {
	if (spec === undefined) return undefined;
	return region === undefined ? Object.values(spec.regions)[0] : spec.regions[region];
}

/** A copy of a deployment spec whose service sizing keeps only the given regions. */
export function specForRegions(spec: DeploymentSpec, regions: readonly string[]): DeploymentSpec {
	if (spec.spec.case !== "service") return spec;
	const service = spec.spec.value;
	const sized = Object.entries(service.regions).filter(([region]) => regions.includes(region));
	return { ...spec, spec: { case: "service", value: { ...service, regions: Object.fromEntries(sized) } } };
}

export function placementIn(d: Deployment | undefined, region: string): DeploymentPlacement | undefined {
	return d?.placements.find((p) => p.region === region);
}

export function deploymentRegions(d: Deployment): string[] {
	return d.placements.map((p) => p.region);
}

/** Whether the deployment is live in any region. */
export function isLive(d: Deployment): boolean {
	return d.placements.some((p) => p.live);
}

/** The first problem any region of the deployment reports. */
export function deploymentProblem(d: Deployment): Problem | undefined {
	return d.placements.find((p) => p.problem !== undefined)?.problem;
}

export function desiredReplicas(d: Deployment): number {
	return d.placements.reduce((sum, p) => sum + p.desiredReplicas, 0);
}

/** The deployment status that matches a placement status. */
export function placementPhase(status: PlacementStatus): DeploymentStatus {
	switch (status) {
		case PlacementStatus.PENDING:
			return DeploymentStatus.BUILDING;
		case PlacementStatus.DEPLOYING:
			return DeploymentStatus.DEPLOYING;
		case PlacementStatus.RUNNING:
			return DeploymentStatus.RUNNING;
		case PlacementStatus.DEGRADED:
			return DeploymentStatus.DEGRADED;
		case PlacementStatus.CRASHED:
			return DeploymentStatus.CRASHED;
		case PlacementStatus.FAILED:
			return DeploymentStatus.FAILED;
		case PlacementStatus.CANCELED:
			return DeploymentStatus.CANCELED;
		case PlacementStatus.SUPERSEDED:
			return DeploymentStatus.SUPERSEDED;
		case PlacementStatus.UNSPECIFIED:
			return DeploymentStatus.UNSPECIFIED;
	}
}

export function deploymentImage(d: Deployment): string {
	return getServiceSpec(d)?.build?.image ?? "";
}

export function isInFlight(phase: DeploymentStatus): boolean {
	switch (phase) {
		case DeploymentStatus.BUILDING:
			return true;
		case DeploymentStatus.DEPLOYING:
			return true;
		case DeploymentStatus.UNSPECIFIED:
			return false;
		case DeploymentStatus.RUNNING:
			return false;
		case DeploymentStatus.DEGRADED:
			return false;
		case DeploymentStatus.CRASHED:
			return false;
		case DeploymentStatus.SUPERSEDED:
			return false;
		case DeploymentStatus.FAILED:
			return false;
		case DeploymentStatus.CANCELED:
			return false;
	}
}

export function deploymentActor(d: Deployment): string {
	return d.actorType === DeploymentActorType.USER ? d.actorName || d.actorId : d.actorName || "Deleted token";
}

export const METHOD_LABEL: Record<DeploymentMethod, string> = {
	[DeploymentMethod.UNSPECIFIED]: "",
	[DeploymentMethod.CLI]: "CLI",
	[DeploymentMethod.WEB]: "Dashboard",
	[DeploymentMethod.API_TOKEN]: "API token",
};

export function deploymentMethod(d: Deployment): string {
	return METHOD_LABEL[d.method];
}

export function shortSha(sha: string): string {
	return sha.slice(0, 7);
}

/** What the deployment runs: its commit, or its image tag. */
export function deploymentRef(d: Deployment): string {
	const sha = d.commit?.sha ?? "";
	return sha === "" ? imageTag(deploymentImage(d)) : shortSha(sha);
}

/** What the deployment does, such as "Deploy 3e8a51d" or "Rollback to v1.2". */
export function deploymentHeadline(d: Deployment, ref = deploymentRef(d)): string {
	switch (d.trigger) {
		case DeploymentTrigger.ROLLBACK:
			return `Rollback to ${ref}`;
		case DeploymentTrigger.REDEPLOY:
			return `Redeploy of ${ref}`;
		case DeploymentTrigger.SCALE:
			return "Settings change";
		case DeploymentTrigger.DEPLOY:
		case DeploymentTrigger.UNSPECIFIED:
			return `Deploy ${ref}`;
	}
}
