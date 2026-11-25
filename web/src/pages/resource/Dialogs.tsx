import { ArrowRightIcon, ChevronsUpDownIcon } from "lucide-react";
import { InlineCodeList, withCode } from "@/components/design/InlineCode";
import { useState } from "react";
import type { Deployment } from "@gen/loco/deployment/v1/deployment_pb";

import { Button } from "@/components/design/Button";
import { DialogBody, DialogClose, DialogContent, DialogFooter } from "@/components/design/Dialog";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuRadioGroup,
	DropdownMenuRadioItem,
	DropdownMenuTrigger,
} from "@/components/design/DropdownMenu";
import { DeploymentStatusBadge } from "@/components/design/StatusBadge";
import { useNow } from "@/hooks/useNow";
import { agoLabel, readyMs } from "@/lib/time";
import { deploymentRegions } from "@/lib/deployment-utils";
import { cn } from "@/lib/utils";

import { depTag } from "./model";

import { specRows } from "./spec";

function DiffCell({ value, tone }: { value: string | null; tone: "add" | "del" | "same" }) {
	const cls =
		value === null
			? "bg-hatch"
			: tone === "add"
				? "bg-ok-bg text-ok-fg"
				: tone === "del"
					? "bg-bad-bg text-bad-fg"
					: "text-fg2";
	return (
		<td className={cn("border-l border-line px-2.5 py-0.5 align-top break-all", cls)}>
			{value ?? <span className="sr-only">not set</span>}
		</td>
	);
}

function RunningSwap({ from, to }: { from: string; to: string }) {
	return (
		<div className="flex items-center gap-3.5 rounded-lg bg-panel px-3.5 py-3">
			<div className="flex min-w-0 flex-col gap-0.5 text-[13px]">
				<span data-slot="eyebrow" className="text-[12px] text-fg3">
					Now running
				</span>
				<span className="truncate font-mono text-foreground">{from}</span>
			</div>
			<ArrowRightIcon aria-hidden className="size-4 shrink-0 text-fg3" />
			<div className="flex min-w-0 flex-col gap-0.5 text-[13px]">
				<span data-slot="eyebrow" className="text-[12px] text-fg3">
					Will run
				</span>
				<span className="truncate font-mono text-foreground">{to}</span>
			</div>
		</div>
	);
}

export function RedeployDialogContent({
	resourceName,
	running,
	pending,
	onConfirm,
}: {
	resourceName: string;
	running: Deployment[];
	pending: boolean;
	onConfirm: () => void;
}) {
	const regions = running.flatMap((d) => d.placements.filter((p) => p.live).map((p) => p.region));
	return (
		<DialogContent title={withCode`Restart ${resourceName}`} showCloseButton={false}>
			<DialogBody className="leading-normal text-fg2">
				<span>
					Replaces every replica in <InlineCodeList names={regions} /> with a fresh one from the same spec. Picks up
					changed secrets.
				</span>
			</DialogBody>
			<DialogFooter>
				<DialogClose render={<Button variant="outline" />}>Cancel</DialogClose>
				<Button disabled={pending} onClick={onConfirm}>
					{pending ? "Restarting…" : "Restart"}
				</Button>
			</DialogFooter>
		</DialogContent>
	);
}

const NO_OPTIONS: readonly Deployment[] = [];

function RollbackOption({ deployment }: { deployment: Deployment }) {
	const started = readyMs(deployment);
	const nowMs = useNow().getTime();
	return (
		<>
			<span className="font-mono text-[12.5px]">{depTag(deployment)}</span>
			<DeploymentStatusBadge phase={deployment.status} />
			<span className="text-[12px] text-fg3">{started === undefined ? "" : agoLabel(started, nowMs)}</span>
		</>
	);
}

/** Picks the deployment to roll back to among the ones the region can run again. */
function RollbackPicker({
	target,
	options,
	onTarget,
}: {
	target: Deployment;
	options: readonly Deployment[];
	onTarget: (deployment: Deployment) => void;
}) {
	return (
		<div className="flex flex-col gap-1.5">
			<span data-slot="eyebrow" className="text-[12px] text-fg3">
				Roll back to
			</span>
			<DropdownMenu>
				<DropdownMenuTrigger
					render={<Button variant="outline" aria-label="Roll back to" className="h-9 justify-start gap-2.5" />}
				>
					<RollbackOption deployment={target} />
					<ChevronsUpDownIcon className="ml-auto text-fg3" />
				</DropdownMenuTrigger>
				<DropdownMenuContent align="start" className="max-h-72 w-auto min-w-[320px]">
					<DropdownMenuRadioGroup
						value={target.id}
						onValueChange={(id: string) => {
							const next = options.find((d) => d.id === id);
							if (next !== undefined) onTarget(next);
						}}
					>
						{options.map((d) => (
							<DropdownMenuRadioItem key={d.id} value={d.id} className="gap-2.5">
								<RollbackOption deployment={d} />
							</DropdownMenuRadioItem>
						))}
					</DropdownMenuRadioGroup>
				</DropdownMenuContent>
			</DropdownMenu>
		</div>
	);
}

export function RollbackDialogContent({
	resourceName,
	target,
	current,
	options = NO_OPTIONS,
	onTarget,
	pending,
	onConfirm,
}: {
	resourceName: string;
	target: Deployment;
	current: Deployment | undefined;
	/** The deployments the region can roll back to, target included; a single one offers no choice. */
	options?: readonly Deployment[];
	onTarget?: (deployment: Deployment) => void;
	pending: boolean;
	onConfirm: () => void;
}) {
	const [everything, setEverything] = useState(false);
	const rows = current === undefined ? [] : specRows(current, target);
	const changes = rows.filter((r) => !r.same);
	const shown = everything ? rows : changes;
	const regions = deploymentRegions(target);
	return (
		<DialogContent size="wide" title={withCode`Roll back ${resourceName}`} showCloseButton={false}>
			<DialogBody className="gap-3.5 leading-normal text-fg2">
				{options.length > 1 && onTarget !== undefined && (
					<RollbackPicker target={target} options={options} onTarget={onTarget} />
				)}
				{current === undefined ? (
					<RunningSwap from="—" to={depTag(target)} />
				) : (
					<div className="flex flex-col overflow-hidden rounded-lg bg-panel">
						<div className="flex items-center justify-between gap-3 border-b border-line px-3.5 py-2.5 text-[12.5px] text-fg3">
							<span>
								{changes.length === 0
									? `No spec changes, ${rows.length.toString()} settings`
									: `${changes.length.toString()} ${changes.length === 1 ? "change" : "changes"}, ${(rows.length - changes.length).toString()} unchanged`}
							</span>
							<Button
								variant="link"
								size="sm"
								className="h-auto p-0 text-[12.5px]"
								onClick={() => {
									setEverything(!everything);
								}}
							>
								{everything ? "Only changes" : "Show everything"}
							</Button>
						</div>
						{shown.length > 0 && (
							<div className="max-h-[min(52vh,420px)] overflow-y-auto">
								<table className="w-full table-fixed border-collapse font-mono text-[12px] leading-[1.6]">
									<colgroup>
										<col className="w-24 sm:w-[190px]" />
										<col />
										<col />
									</colgroup>
									<thead className="sticky top-0 bg-panel align-bottom font-sans text-[11.5px] text-fg3">
										<tr className="border-b border-line">
											<th scope="col" className="py-2 pl-3.5 text-left font-normal">
												<span className="sr-only">Setting</span>
											</th>
											<th scope="col" className="px-2.5 py-2 text-left font-normal">
												Now running <span className="font-mono text-foreground">{depTag(current)}</span>
											</th>
											<th scope="col" className="px-2.5 py-2 text-left font-normal">
												Will run <span className="font-mono text-foreground">{depTag(target)}</span>
											</th>
										</tr>
									</thead>
									<tbody>
										{shown.map((c) => (
											<tr key={c.label}>
												<th scope="row" className="py-0.5 pl-3.5 text-left align-top font-normal break-all text-fg3">
													{c.label}
												</th>
												<DiffCell value={c.from} tone={c.same ? "same" : "del"} />
												<DiffCell value={c.to} tone={c.same ? "same" : "add"} />
											</tr>
										))}
									</tbody>
								</table>
							</div>
						)}
					</div>
				)}
				<span>
					Creates a new deployment in <InlineCodeList names={regions} /> from the spec of{" "}
					<span className="font-mono text-foreground">{depTag(target)}</span>.
					{current !== undefined && " The running deployment is replaced once the new replicas are ready."}
				</span>
			</DialogBody>
			<DialogFooter>
				<DialogClose render={<Button variant="outline" />}>Cancel</DialogClose>
				<Button disabled={pending} onClick={onConfirm}>
					{pending ? "Rolling back…" : "Roll back"}
				</Button>
			</DialogFooter>
		</DialogContent>
	);
}
