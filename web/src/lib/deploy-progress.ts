import { DeploymentPhase } from "@gen/loco/deployment/v1/deployment_pb";

export type StepState = "done" | "active" | "failed" | "todo";

export interface ProgressTarget {
	name: string;
	image: string;
	min: number;
	cpu: string;
	memory: string;
	hcPath: string;
	hcInterval: string;
	resourceId?: string | undefined;
	deploymentId?: string | undefined;
}

export interface ProgressInput {
	target: ProgressTarget;
	region: string;
	envName: string;
	phase: DeploymentPhase | undefined;
	message: string;
	error: string | null;
	submitting: boolean;
}

export function deployOutcome(phase: DeploymentPhase | undefined): "live" | "failed" | null {
	switch (phase) {
		case DeploymentPhase.RUNNING:
			return "live";
		case DeploymentPhase.SUCCEEDED:
			return "live";
		case DeploymentPhase.FAILED:
			return "failed";
		case DeploymentPhase.CANCELED:
			return "failed";
		case DeploymentPhase.PENDING:
			return null;
		case DeploymentPhase.DEPLOYING:
			return null;
		case DeploymentPhase.UNSPECIFIED:
			return null;
		case undefined:
			return null;
	}
}

export function progressSteps({ target, region, envName, phase, message, error, submitting }: ProgressInput) {
	const created = target.resourceId !== undefined;
	const queued = target.deploymentId !== undefined;
	const failedPhase = phase === DeploymentPhase.FAILED || phase === DeploymentPhase.CANCELED;
	const live = phase === DeploymentPhase.RUNNING || phase === DeploymentPhase.SUCCEEDED;
	const rolling = phase === DeploymentPhase.DEPLOYING;

	const createState: StepState = created ? "done" : error !== null ? "failed" : submitting ? "active" : "todo";
	const queueState: StepState = !created
		? "todo"
		: !queued
			? error !== null
				? "failed"
				: "active"
			: rolling || live
				? "done"
				: failedPhase
					? "failed"
					: "active";
	const rollState: StepState = live ? "done" : rolling ? "active" : "todo";
	const healthState: StepState = live ? "done" : "todo";

	const image = target.image.split("/").pop() ?? target.image;
	const failMeta = error ?? message;
	return [
		{
			label: "Resource created",
			state: createState,
			meta: createState === "failed" ? failMeta : `${target.name} · ${envName}`,
		},
		{
			label: "Deployment queued",
			state: queueState,
			meta: queueState === "failed" ? failMeta : queueState === "active" && message !== "" ? message : region,
		},
		{
			label: "Rolling out",
			state: rollState,
			meta:
				rolling && message !== ""
					? message
					: `${image} · ${target.min.toString()} × ${target.cpu} CPU · ${target.memory}`,
		},
		{
			label: "Health checks passing",
			state: healthState,
			meta: target.hcPath === "" ? "No HTTP health check" : `${target.hcPath} every ${target.hcInterval}s`,
		},
	];
}
