import { DeploymentStatus } from "@gen/loco/deployment/v1/deployment_pb";

export type RunOutcome = "deploying" | "live" | "failed";

export function runOutcome(
	regions: readonly { phase: DeploymentStatus | undefined; error: string | null }[],
): RunOutcome {
	if (regions.length === 0) return "deploying";
	if (regions.every((r) => r.phase === DeploymentStatus.RUNNING || r.phase === DeploymentStatus.SUPERSEDED))
		return "live";
	if (
		regions.every(
			(r) => r.error !== null || r.phase === DeploymentStatus.FAILED || r.phase === DeploymentStatus.CANCELED,
		)
	)
		return "failed";
	return "deploying";
}
