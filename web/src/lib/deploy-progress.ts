import { DeploymentPhase } from "@gen/loco/deployment/v1/deployment_pb";

import { healthSummary, type NewResourceForm } from "./new-resource";

export type StepState = "done" | "active" | "failed" | "todo";

export interface ProgressStep {
	label: string;
	state: StepState;
	meta: string;
}

const DEPLOYING_STEPS = 2;

export function isLive(phase: DeploymentPhase | undefined): boolean {
	return phase === DeploymentPhase.RUNNING || phase === DeploymentPhase.SUCCEEDED;
}

export interface RegionProgress {
	form: NewResourceForm;
	created: boolean;
	region: string;
	phase: DeploymentPhase | undefined;
	message: string;
	error: string | null;
}

function stepState(i: number, { created, phase, error }: RegionProgress): StepState {
	if (isLive(phase)) return "done";
	if (error !== null || phase === DeploymentPhase.FAILED || phase === DeploymentPhase.CANCELED) {
		return i === 0 ? "failed" : "todo";
	}
	if (!created) return "todo";
	const active = phase === DeploymentPhase.DEPLOYING ? DEPLOYING_STEPS : 1;
	return i < active ? "active" : "todo";
}

export function regionDeploySteps(progress: RegionProgress): ProgressStep[] {
	const { form, region, message, error } = progress;
	const image = form.image.trim();
	const steps: [string, string][] = [
		["Pull image", image.split("/").pop() ?? image],
		["Start replicas", `${form.replicas.toString()} × ${form.cpu} CPU · ${form.memory}`],
		["Health checks", form.health.path === "" ? "No HTTP health check" : healthSummary(form.health)],
		["Route traffic", form.isPublic ? `${form.subdomain}.${form.platformDomain}` : "Internal"],
		["Live", region],
	];
	const states = steps.map((_, i) => stepState(i, progress));
	const current = states.lastIndexOf("active");
	return steps.map(([label, detail], i) => {
		const state = states[i] ?? "todo";
		const meta = state === "failed" ? (error ?? message) : i === current && message !== "" ? message : detail;
		return { label, state, meta };
	});
}
