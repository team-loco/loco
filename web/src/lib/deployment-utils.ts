import {
	DeploymentActorType,
	DeploymentMethod,
	DeploymentPhase,
	type Deployment,
	type ServiceDeploymentSpec,
} from "@gen/loco/deployment/v1/deployment_pb";

export function getServiceSpec(deployment: Deployment): ServiceDeploymentSpec | undefined {
	if (!deployment.spec?.spec) {
		return undefined;
	}

	if (deployment.spec.spec.case === "service") {
		return deployment.spec.spec.value;
	}

	return undefined;
}

export function deploymentImage(d: Deployment): string {
	return getServiceSpec(d)?.build?.image ?? "";
}

export function isInFlight(phase: DeploymentPhase): boolean {
	switch (phase) {
		case DeploymentPhase.PENDING:
			return true;
		case DeploymentPhase.DEPLOYING:
			return true;
		case DeploymentPhase.UNSPECIFIED:
			return false;
		case DeploymentPhase.RUNNING:
			return false;
		case DeploymentPhase.SUCCEEDED:
			return false;
		case DeploymentPhase.FAILED:
			return false;
		case DeploymentPhase.CANCELED:
			return false;
	}
}

export function deploymentActor(d: Deployment): string {
	return d.actorType === DeploymentActorType.USER ? `@${d.actorName || d.actorId}` : d.actorName || "Deleted token";
}

export function deploymentOrigin(d: Deployment): string {
	const actor = deploymentActor(d);
	const methods: Record<DeploymentMethod, string> = {
		[DeploymentMethod.UNSPECIFIED]: "Unknown method",
		[DeploymentMethod.CLI]: "loco deploy",
		[DeploymentMethod.WEB]: "Dashboard",
		[DeploymentMethod.API_TOKEN]: "API token",
	};
	const parts = [actor, methods[d.method]];
	if (d.commit !== undefined) {
		parts.push(`${d.commit.sha.slice(0, 7)}${d.commit.dirty ? " (uncommitted changes)" : ""}`);
	}
	return parts.join(" · ");
}
