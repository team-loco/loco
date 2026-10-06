import type {
	Deployment,
	ServiceDeploymentSpec,
} from "@gen/loco/deployment/v1/deployment_pb";

export function getServiceSpec(
	deployment: Deployment
): ServiceDeploymentSpec | undefined {
	if (!deployment.spec?.spec) {
		return undefined;
	}

	if (deployment.spec.spec.case === "service") {
		return deployment.spec.spec.value;
	}

	return undefined;
}
