import { DeploymentPhase } from "@gen/loco/deployment/v1/deployment_pb";
import { ResourceStatus } from "@gen/loco/resource/v1/resource_pb";

import { type Glyph, StatusGlyph } from "./StatusGlyph";

interface StatusStyle {
	label: string;
	glyph: Glyph;
}

function resourceStatusStyle(status: ResourceStatus | undefined): StatusStyle {
	switch (status) {
		case ResourceStatus.HEALTHY:
			return { label: "Healthy", glyph: "ok" };
		case ResourceStatus.DEPLOYING:
			return { label: "Deploying", glyph: "spin" };
		case ResourceStatus.DEGRADED:
			return { label: "Degraded", glyph: "warn" };
		case ResourceStatus.UNAVAILABLE:
			return { label: "Unavailable", glyph: "err" };
		case ResourceStatus.UNKNOWN:
			return { label: "Unknown", glyph: "off" };
		case ResourceStatus.UNSPECIFIED:
			return { label: "Pending", glyph: "draft" };
		case undefined:
			return { label: "Pending", glyph: "draft" };
	}
}

function deploymentPhaseStyle(phase: DeploymentPhase | undefined): StatusStyle {
	switch (phase) {
		case DeploymentPhase.PENDING:
			return { label: "Pending", glyph: "draft" };
		case DeploymentPhase.DEPLOYING:
			return { label: "Deploying", glyph: "spin" };
		case DeploymentPhase.RUNNING:
			return { label: "Running", glyph: "ok" };
		case DeploymentPhase.SUCCEEDED:
			return { label: "Succeeded", glyph: "off" };
		case DeploymentPhase.FAILED:
			return { label: "Failed", glyph: "err" };
		case DeploymentPhase.CANCELED:
			return { label: "Canceled", glyph: "off" };
		case DeploymentPhase.UNSPECIFIED:
			return { label: "Pending", glyph: "draft" };
		case undefined:
			return { label: "Pending", glyph: "draft" };
	}
}

export function StatusLabel({ glyph, label }: StatusStyle) {
	return (
		<span className="inline-flex items-center gap-2 text-[13.5px] whitespace-nowrap text-foreground">
			<StatusGlyph glyph={glyph} />
			{label}
		</span>
	);
}

export function ResourceStatusBadge({ status }: { status: ResourceStatus | undefined }) {
	return <StatusLabel {...resourceStatusStyle(status)} />;
}

export function DeploymentPhaseBadge({ phase }: { phase: DeploymentPhase | undefined }) {
	return <StatusLabel {...deploymentPhaseStyle(phase)} />;
}
