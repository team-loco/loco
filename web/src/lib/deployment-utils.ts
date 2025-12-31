import type {
	Deployment,
	ServiceDeploymentSpec,
} from "@/gen/deployment/v1/deployment_pb";
import { DeploymentPhase } from "@/gen/deployment/v1/deployment_pb";

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

export function getPhaseTooltip(phase: DeploymentPhase): string {
	const tooltips: Record<DeploymentPhase, string> = {
		[DeploymentPhase.UNSPECIFIED]: "Unknown status",
		[DeploymentPhase.PENDING]:
			"Waiting for the deployment to start. Your app is queued and ready to go.",
		[DeploymentPhase.DEPLOYING]:
			"In progress. We're pulling your image, creating pods, and getting everything ready.",
		[DeploymentPhase.RUNNING]:
			"Live and healthy. App Traffic points to this deployment.",
		[DeploymentPhase.SUCCEEDED]:
			"Completed successfully. The deployment finished without any issues.",
		[DeploymentPhase.FAILED]:
			"Hit a snag. Something went wrong during deployment or runtime.",
		[DeploymentPhase.CANCELED]:
			"Stopped by you. This deployment was manually cancelled.",
	};
	return tooltips[phase] || "Unknown status";
}

export function getResourceStatusTooltip(statusLabel: string): string {
	const tooltips: Record<string, string> = {
		running: "Your app is live and healthy. It's up and serving traffic.",
		deploying: "Your app is being deployed. We're pulling the image and creating pods.",
		degraded: "Your app has issues but is still partially operational. Check the logs for details.",
		unavailable: "Your app is currently unavailable. A deployment may be in progress or an error occurred.",
		stopped: "Your app is stopped and not running.",
		pending: "Waiting to deploy. Your app is queued.",
		failed: "Deployment failed. Check the logs for more information.",
	};
	return tooltips[statusLabel.toLowerCase()] || "Unknown status";
}
