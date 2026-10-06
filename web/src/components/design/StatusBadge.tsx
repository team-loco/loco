import { DeploymentPhase } from "@gen/loco/deployment/v1/deployment_pb";
import { ResourceStatus } from "@gen/loco/resource/v1/resource_pb";

import { Badge, type BadgeTone } from "./Badge";

interface StatusStyle {
	label: string;
	tone: BadgeTone;
}

export function effectiveResourceStatus(status: ResourceStatus, hasDeployments: boolean): ResourceStatus {
	return hasDeployments ? status : ResourceStatus.UNSPECIFIED;
}

export function resourceStatusStyle(status: ResourceStatus | undefined): StatusStyle {
	switch (status) {
		case ResourceStatus.HEALTHY:
			return { label: "Healthy", tone: "ok" };
		case ResourceStatus.DEPLOYING:
			return { label: "Deploying", tone: "info" };
		case ResourceStatus.DEGRADED:
			return { label: "Degraded", tone: "warn" };
		case ResourceStatus.UNAVAILABLE:
			return { label: "Unavailable", tone: "bad" };
		case ResourceStatus.SUSPENDED:
			return { label: "Suspended", tone: "neutral" };
		case ResourceStatus.UNSPECIFIED:
			return { label: "Pending", tone: "neutral" };
		case undefined:
			return { label: "Pending", tone: "neutral" };
	}
}

function deploymentPhaseStyle(phase: DeploymentPhase | undefined): StatusStyle {
	switch (phase) {
		case DeploymentPhase.PENDING:
			return { label: "Pending", tone: "neutral" };
		case DeploymentPhase.DEPLOYING:
			return { label: "Deploying", tone: "info" };
		case DeploymentPhase.RUNNING:
			return { label: "Running", tone: "ok" };
		case DeploymentPhase.SUCCEEDED:
			return { label: "Succeeded", tone: "neutral" };
		case DeploymentPhase.FAILED:
			return { label: "Failed", tone: "bad" };
		case DeploymentPhase.CANCELED:
			return { label: "Canceled", tone: "neutral" };
		case DeploymentPhase.UNSPECIFIED:
			return { label: "Pending", tone: "neutral" };
		case undefined:
			return { label: "Pending", tone: "neutral" };
	}
}

export function ResourceStatusBadge({ status, size }: { status: ResourceStatus | undefined; size?: "default" | "sm" }) {
	const s = resourceStatusStyle(status);
	return (
		<Badge tone={s.tone} size={size}>
			{s.label}
		</Badge>
	);
}

export function DeploymentPhaseBadge({ phase, size }: { phase: DeploymentPhase | undefined; size?: "default" | "sm" }) {
	const s = deploymentPhaseStyle(phase);
	return (
		<Badge tone={s.tone} size={size}>
			{s.label}
		</Badge>
	);
}
