import { DeploymentPhase } from "@gen/loco/deployment/v1/deployment_pb";

import { healthSummary, type NewResourceForm } from "./new-resource";

export type StepState = "done" | "active" | "failed" | "todo";

export interface ProgressStep {
	label: string;
	state: StepState;
	meta: string;
}

const DEPLOYING_STEPS = 2;

function isLive(phase: DeploymentPhase | undefined): boolean {
	return phase === DeploymentPhase.RUNNING || phase === DeploymentPhase.SUCCEEDED;
}

export interface RegionProgress {
	form: Omit<NewResourceForm, "vars">;
	created: boolean;
	region: string;
	phase: DeploymentPhase | undefined;
	message: string;
	error: string | null;
}

function stepState(i: number, { created, phase }: RegionProgress): StepState {
	if (isLive(phase)) return "done";
	if (!created) return "todo";
	const active = phase === DeploymentPhase.DEPLOYING ? DEPLOYING_STEPS : 1;
	return i < active ? "active" : "todo";
}

export function regionDeploySteps(progress: RegionProgress): ProgressStep[] {
	const { form, region, phase, message, error } = progress;
	const image = form.image.trim();
	const steps: [string, string][] = [
		["Pull image", image.split("/").pop() ?? image],
		["Start replicas", `${form.replicas.toString()} × ${form.cpu} CPU · ${form.memory}`],
		["Health checks", form.health.path === "" ? "No HTTP health check" : healthSummary(form.health)],
		["Route traffic", form.isPublic ? `${form.subdomain}.${form.platformDomain}` : "Internal"],
		["Live", region],
	];
	const canceled = phase === DeploymentPhase.CANCELED;
	if (error !== null || phase === DeploymentPhase.FAILED || canceled) {
		return [
			...steps.map(([label, meta]): ProgressStep => ({ label, state: "todo", meta })),
			{ label: canceled ? "Canceled" : "Failed", state: "failed", meta: error ?? message },
		];
	}
	const states = steps.map((_, i) => stepState(i, progress));
	const current = states.lastIndexOf("active");
	return steps.map(([label, detail], i) => {
		const state = states[i] ?? "todo";
		return { label, state, meta: i === current && message !== "" ? message : detail };
	});
}

export type RunOutcome = "deploying" | "live" | "failed";

export function runOutcome(regions: readonly (readonly ProgressStep[])[]): RunOutcome {
	if (regions.length === 0) return "deploying";
	if (regions.every((steps) => steps.every((s) => s.state === "done"))) return "live";
	if (regions.every((steps) => steps.some((s) => s.state === "failed"))) return "failed";
	return "deploying";
}
