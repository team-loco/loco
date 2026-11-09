import { DeploymentPhase } from "@gen/loco/deployment/v1/deployment_pb";

import type { StepState } from "@/lib/deploy-progress";

export interface RolloutStep {
	label: string;
	state: StepState;
	detail: string;
}

const QUEUED = 0;
const ROLLING = 1;
const READY = 2;
const DONE = 4;

function progressIndex(phase: DeploymentPhase | undefined, ready: number, desired: number): number {
	switch (phase) {
		case DeploymentPhase.PENDING:
			return QUEUED;
		case DeploymentPhase.DEPLOYING:
			return desired > 0 && ready >= desired ? READY : ROLLING;
		case DeploymentPhase.RUNNING:
			return DONE;
		case DeploymentPhase.SUCCEEDED:
			return DONE;
		case DeploymentPhase.FAILED:
			return ROLLING;
		case DeploymentPhase.CANCELED:
			return ROLLING;
		case DeploymentPhase.UNSPECIFIED:
			return QUEUED;
		case undefined:
			return QUEUED;
	}
}

function stateAt(index: number, current: number, failed: boolean): StepState {
	if (index < current) return "done";
	if (index > current) return "todo";
	return failed ? "failed" : "active";
}

export function rolloutSteps(
	phase: DeploymentPhase | undefined,
	message: string,
	ready: number,
	desired: number,
): RolloutStep[] {
	const current = progressIndex(phase, ready, desired);
	const failed = phase === DeploymentPhase.FAILED || phase === DeploymentPhase.CANCELED;
	const steps = [
		{ label: "Queued", detail: current === QUEUED ? message : "" },
		{ label: "Rolling out", detail: current === ROLLING ? message : "" },
		{ label: "Replicas ready", detail: `${ready.toString()}/${desired.toString()} ready` },
		{ label: "Running", detail: "" },
	];
	return steps.map((step, i) => ({ ...step, state: stateAt(i, current, failed) }));
}
