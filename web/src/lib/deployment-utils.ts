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

const METHOD_LABEL: Record<DeploymentMethod, string> = {
	[DeploymentMethod.UNSPECIFIED]: "",
	[DeploymentMethod.CLI]: "loco deploy · CLI",
	[DeploymentMethod.WEB]: "Dashboard",
	[DeploymentMethod.API_TOKEN]: "API token",
};

export function deploymentMethod(d: Deployment): string {
	return METHOD_LABEL[d.method];
}

export function shortSha(sha: string): string {
	return sha.slice(0, 7);
}
