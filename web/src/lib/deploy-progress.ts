import { DeploymentPhase } from "@gen/loco/deployment/v1/deployment_pb";

import type { NewResourceForm } from "./new-resource";

export type StepState = "done" | "active" | "failed" | "todo";

export interface ProgressStep {
	label: string;
	state: StepState;
	meta: string;
}

export function isLive(phase: DeploymentPhase | undefined): boolean {
	return phase === DeploymentPhase.RUNNING || phase === DeploymentPhase.SUCCEEDED;
}

export function createdStep(name: string, envName: string, created: boolean, submitting: boolean): ProgressStep {
	return {
		label: "Resource created",
		state: created ? "done" : submitting ? "active" : "todo",
		meta: `${name} · ${envName}`,
	};
}

export interface RegionProgress {
	form: NewResourceForm;
	hcInterval: string;
	created: boolean;
	deploymentId: string | undefined;
	region: string;
	phase: DeploymentPhase | undefined;
	message: string;
	error: string | null;
}

export function regionDeploySteps({
	form,
	hcInterval,
	created,
	deploymentId,
	region,
	phase,
	message,
	error,
}: RegionProgress): ProgressStep[] {
	const queued = deploymentId !== undefined;
	const failedPhase = phase === DeploymentPhase.FAILED || phase === DeploymentPhase.CANCELED;
	const live = isLive(phase);
	const rolling = phase === DeploymentPhase.DEPLOYING;

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

	const image = form.image.trim();
	const imageName = image.split("/").pop() ?? image;
	const failMeta = error ?? message;
	return [
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
					: `${imageName} · ${form.replicas.toString()} × ${form.cpu} CPU · ${form.memory}`,
		},
		{
			label: "Health checks passing",
			state: healthState,
			meta: form.healthPath === "" ? "No HTTP health check" : `${form.healthPath} every ${hcInterval}s`,
		},
	];
}
