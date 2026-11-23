import { CheckIcon, ChevronDownIcon, ChevronUpIcon, MinusIcon, XIcon } from "lucide-react";
import type { ReactNode } from "react";

import { CopyId } from "@/components/design/CopyId";
import { cn, formatShortId } from "@/lib/utils";

import type { FlowState } from "./deployment-progress";

export interface FlowNode {
	key: string;
	label: string;
	state: FlowState;
	/** How long the step took. */
	duration: string;
	/** What the step reports about itself, next to the duration. */
	extra: string;
}

const RING = "shadow-[inset_0_0_0_1.5px_var(--line2)]";

function StepGlyph({ state }: { state: FlowState }) {
	return (
		<span
			aria-hidden
			className={cn(
				"grid size-[18px] shrink-0 place-items-center rounded-full text-fg2",
				state === "done" ? "bg-panel2" : RING,
				state === "failed" && "bg-[color-mix(in_oklch,var(--err)_14%,transparent)] text-err shadow-none",
				(state === "skipped" || state === "stopped") && "text-fg4",
			)}
		>
			{state === "done" && <CheckIcon className="size-[11px]" />}
			{state === "failed" && <XIcon className="size-[11px]" />}
			{(state === "skipped" || state === "stopped") && <MinusIcon className="size-[11px]" />}
			{state === "active" && <i className="size-2 rounded-full bg-info" />}
			{state === "retry" && <i className="size-2 rounded-full bg-warn" />}
		</span>
	);
}

function FlowStep({ node, open, onToggle }: { node: FlowNode; open: boolean; onToggle: (key: string) => void }) {
	const { state } = node;
	const idle = state === "pending" || state === "skipped";
	return (
		<button
			type="button"
			aria-expanded={open}
			disabled={idle}
			onClick={() => {
				onToggle(node.key);
			}}
			className="inline-flex h-8 items-center gap-2 rounded-lg border-0 bg-transparent pr-2.5 pl-1.5 text-[13px] text-foreground enabled:hover:bg-[color-mix(in_oklch,var(--fg)_6%,transparent)] aria-expanded:bg-[color-mix(in_oklch,var(--fg)_6%,transparent)] disabled:cursor-default"
		>
			<StepGlyph state={state} />
			<span
				className={cn(
					"font-medium",
					idle && "font-normal text-fg3",
					state === "failed" && "text-err",
					state === "retry" && "text-warn",
				)}
			>
				{node.label}
				<span className="sr-only">, {state}</span>
			</span>
			{node.duration !== "" && <span className="font-mono text-[11.5px] text-fg3">{node.duration}</span>}
			{node.extra !== "" && (
				<span className="ml-0.5 border-l border-line2 pl-2 font-mono text-[11.5px] text-fg3">{node.extra}</span>
			)}
			{!idle && (
				<span aria-hidden className="flex text-fg3">
					{open ? <ChevronUpIcon className="size-3" /> : <ChevronDownIcon className="size-3" />}
				</span>
			)}
		</button>
	);
}

function Line() {
	return <span aria-hidden className="h-px w-6 flex-none bg-line2" />;
}

const FAN_BRANCH =
	"relative before:absolute before:top-1/2 before:-left-[18px] before:h-px before:w-[18px] before:bg-line2 after:absolute after:top-0 after:-bottom-0.5 after:-left-[18px] after:w-px after:bg-line2 first:after:top-1/2 last:after:bottom-1/2";

/** The rollout as a graph: the deployment, its build, and one deploy step per region. A step opens its details. */
export function RolloutFlow({
	triggeredBy,
	deploymentId,
	build,
	lanes,
	open,
	onToggle,
}: {
	triggeredBy: string;
	deploymentId: string;
	build: FlowNode;
	/** One deploy step per region, with its region's name. */
	lanes: { region: string; node: FlowNode }[];
	open: string | null;
	onToggle: (key: string) => void;
}): ReactNode {
	const multi = lanes.length > 1;
	return (
		<div role="group" aria-label="Rollout" className="flex flex-wrap items-center justify-center gap-y-1.5 py-1.5">
			<div className="flex min-w-0 flex-col gap-0.5 px-1 text-[13px]">
				<span>{triggeredBy}</span>
				<CopyId
					value={deploymentId}
					title="Copy deployment ID"
					className="-ml-1.5 self-start font-mono text-[12px] font-normal text-fg3"
				>
					{formatShortId(deploymentId)}
				</CopyId>
			</div>
			<Line />
			<FlowStep node={build} open={open === build.key} onToggle={onToggle} />
			<Line />
			<ul aria-label="Regions" className={cn("m-0 flex list-none flex-col p-0", multi ? "gap-2 pl-[18px]" : "gap-0.5")}>
				{lanes.map(({ region, node }) => (
					<li key={region} className={cn("flex items-center", multi && FAN_BRANCH)}>
						{multi && <span className="w-[13ch] flex-none pl-1 font-mono text-[12.5px] text-fg2">{region}</span>}
						<FlowStep node={node} open={open === node.key} onToggle={onToggle} />
					</li>
				))}
			</ul>
		</div>
	);
}
