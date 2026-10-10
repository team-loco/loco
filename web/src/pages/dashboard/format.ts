import { DeploymentPhase, type Deployment } from "@gen/loco/deployment/v1/deployment_pb";
import { ResourceStatus } from "@gen/loco/resource/v1/resource_pb";

import { getServiceSpec } from "@/lib/deployment-utils";

export function deploymentImage(d: Deployment): string {
	return getServiceSpec(d)?.build?.image ?? "";
}

export function agoLabel(ms: number, nowMs: number): string {
	if (ms === 0) return "—";
	const mins = Math.floor((nowMs - ms) / 60_000);
	if (mins < 1) return "just now";
	if (mins < 60) return `${mins.toString()} min ago`;
	const hrs = Math.floor(mins / 60);
	if (hrs < 24) return `${hrs.toString()} hour${hrs === 1 ? "" : "s"} ago`;
	const days = Math.floor(hrs / 24);
	if (days === 1) return "yesterday";
	return `${days.toString()} days ago`;
}

export function shortAgo(ms: number, nowMs: number): string {
	if (ms === 0) return "";
	const mins = Math.floor((nowMs - ms) / 60_000);
	if (mins < 1) return "now";
	if (mins < 60) return `${mins.toString()} min`;
	const hrs = Math.floor(mins / 60);
	if (hrs < 24) return `${hrs.toString()} h`;
	return `${Math.floor(hrs / 24).toString()} d`;
}

const startedFormat = new Intl.DateTimeFormat(undefined, {
	month: "short",
	day: "numeric",
	hour: "2-digit",
	minute: "2-digit",
	hour12: false,
});

export function startedLabel(ms: number): string {
	return ms === 0 ? "—" : startedFormat.format(ms);
}

export function statusRank(status: ResourceStatus): number {
	switch (status) {
		case ResourceStatus.DEGRADED:
			return 0;
		case ResourceStatus.UNAVAILABLE:
			return 0;
		case ResourceStatus.DEPLOYING:
			return 1;
		case ResourceStatus.UNSPECIFIED:
			return 2;
		case ResourceStatus.HEALTHY:
			return 3;
		case ResourceStatus.SUSPENDED:
			return 4;
	}
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
