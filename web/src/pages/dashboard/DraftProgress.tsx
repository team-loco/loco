import { CircleCheckIcon, CircleIcon, CircleXIcon, LoaderCircleIcon } from "lucide-react";
import { DeploymentPhase } from "@gen/loco/deployment/v1/deployment_pb";

import { cn } from "@/lib/utils";

import type { Draft } from "./drafts";

export type StepState = "done" | "active" | "failed" | "todo";

export interface ProgressInput {
	draft: Draft;
	region: string;
	envName: string;
	phase: DeploymentPhase | undefined;
	message: string;
	error: string | null;
	submitting: boolean;
}

export function progressSteps({ draft, region, envName, phase, message, error, submitting }: ProgressInput) {
	const created = draft.resourceId !== undefined;
	const queued = draft.deploymentId !== undefined;
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

	const image = draft.image.split("/").pop() ?? draft.image;
	const failMeta = error ?? message;
	return [
		{
			label: "Resource created",
			state: createState,
			meta: createState === "failed" ? failMeta : `${draft.name} · ${envName}`,
		},
		{
			label: "Deployment queued",
			state: queueState,
			meta: queueState === "failed" ? failMeta : queueState === "active" && message !== "" ? message : region,
		},
		{
			label: "Rolling out",
			state: rollState,
			meta: rolling && message !== "" ? message : `${image} · ${draft.min.toString()} × ${draft.cpu} CPU · ${draft.memory}`,
		},
		{
			label: "Health checks passing",
			state: healthState,
			meta: `${draft.hcPath} every ${draft.hcInterval}s`,
		},
	];
}

function StepIcon({ state }: { state: StepState }) {
	switch (state) {
		case "done":
			return <CircleCheckIcon className="size-4 text-ok-fg" />;
		case "active":
			return <LoaderCircleIcon className="size-4 animate-spin text-info-fg" />;
		case "failed":
			return <CircleXIcon className="size-4 text-bad-fg" />;
		case "todo":
			return <CircleIcon className="size-4 text-fg4" />;
	}
}

export function DraftProgress({
	title,
	elapsed,
	steps,
}: {
	title: string;
	elapsed: string;
	steps: { label: string; state: StepState; meta: string }[];
}) {
	return (
		<div className="flex min-h-0 flex-1 flex-col gap-3 overflow-y-auto px-6 py-5">
			<div className="flex items-center justify-between">
				<span className="font-semibold">{title}</span>
				<span className="text-fg3 tabular-nums">{elapsed}</span>
			</div>
			<div className="flex flex-col">
				{steps.map((s) => {
					const shown = s.state !== "todo";
					return (
						<div
							key={s.label}
							className={cn("grid grid-cols-[20px_minmax(0,1fr)] gap-3 py-2.5", !shown && "opacity-45")}
						>
							<span className="flex pt-px">
								<StepIcon state={s.state} />
							</span>
							<div className="flex min-w-0 flex-col gap-0.5">
								<span className={s.state === "active" ? "font-semibold" : undefined}>{s.label}</span>
								<span className="truncate text-sm text-fg3">{shown ? s.meta : ""}</span>
							</div>
						</div>
					);
				})}
			</div>
		</div>
	);
}
