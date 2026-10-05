import { ChevronDownIcon } from "lucide-react";
import { DeploymentPhase, type Deployment } from "@gen/loco/deployment/v1/deployment_pb";

import { Button } from "@/components/design/Button";
import { Code } from "@/components/design/CodeBlock";
import { DialogContent, DialogFooter } from "@/components/design/Dialog";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuRadioGroup,
	DropdownMenuRadioItem,
	DropdownMenuTrigger,
} from "@/components/design/DropdownMenu";
import { cn } from "@/lib/utils";

import { formatStarted, shortId } from "./format";
import { depTag, startedMs, type RegionView } from "./model";
import { diffLines, specText, type DiffLine } from "./spec";

function optionLabel(dep: Deployment, isCurrent: boolean): string {
	const parts = [shortId(dep.id), depTag(dep), formatStarted(startedMs(dep))];
	if (isCurrent) parts.push("current");
	return parts.join(" · ");
}

function DeploymentPicker({
	region,
	value,
	onChange,
}: {
	region: RegionView;
	value: Deployment;
	onChange: (id: string) => void;
}) {
	return (
		<DropdownMenu>
			<DropdownMenuTrigger render={<Button variant="outline" size="sm" className="max-w-[300px] gap-1.5 px-2" />}>
				<span className="truncate">{optionLabel(value, region.current?.id === value.id)}</span>
				<ChevronDownIcon className="size-3 text-fg3" />
			</DropdownMenuTrigger>
			<DropdownMenuContent className="w-auto min-w-[300px]">
				<DropdownMenuRadioGroup
					value={value.id}
					onValueChange={(v: string) => {
						onChange(v);
					}}
				>
					{region.history.map((d) => (
						<DropdownMenuRadioItem key={d.id} value={d.id} className="text-sm">
							{optionLabel(d, region.current?.id === d.id)}
						</DropdownMenuRadioItem>
					))}
				</DropdownMenuRadioGroup>
			</DropdownMenuContent>
		</DropdownMenu>
	);
}

function lineClass(sign: DiffLine["sign"]): string {
	switch (sign) {
		case "+":
			return "bg-diff-add text-ok-fg";
		case "−":
			return "bg-diff-del text-bad-fg";
		case " ":
			return "text-fg2";
	}
}

export function DiffDialogContent({
	region,
	fromId,
	toId,
	onChange,
	onRollback,
}: {
	region: RegionView;
	fromId: string;
	toId: string;
	onChange: (fromId: string, toId: string) => void;
	onRollback: (target: Deployment, current: Deployment | undefined) => void;
}) {
	const hist = region.history;
	const from = hist.find((d) => d.id === fromId) ?? hist[1] ?? hist[0];
	const to = hist.find((d) => d.id === toId) ?? hist[0];
	if (from === undefined || to === undefined) return null;
	const diff = diffLines(specText(from), specText(to));
	const canRollback = from.id !== region.current?.id && from.status !== DeploymentPhase.FAILED && from.spec !== undefined;

	return (
		<DialogContent
			className="w-[860px]"
			title={
				<span className="flex items-baseline gap-2.5">
					What changed
					<span className="text-sm font-normal text-fg3">{region.name}</span>
				</span>
			}
		>
			<div className="flex flex-wrap items-center gap-2 border-b border-line px-4 py-2.5 text-sm text-fg3">
				<span className="font-semibold text-bad-fg">−</span>
				<DeploymentPicker
					region={region}
					value={from}
					onChange={(id) => {
						onChange(id, to.id);
					}}
				/>
				<span className="ml-1.5 font-semibold text-ok-fg">+</span>
				<DeploymentPicker
					region={region}
					value={to}
					onChange={(id) => {
						onChange(from.id, id);
					}}
				/>
				<div className="flex-1" />
				<span className="tabular-nums">
					+{diff.add} −{diff.del}
				</span>
			</div>
			<div className="max-h-[60vh] min-h-0 overflow-auto py-1 font-mono text-sm leading-[1.6]">
				{diff.lines.map((ln, i) => (
					<div key={i} className={cn("grid grid-cols-[18px_minmax(0,1fr)] pr-4 pl-3", lineClass(ln.sign))}>
						<span className="text-fg4">{ln.sign}</span>
						<Code code={ln.text} language="json" className="whitespace-pre" />
					</div>
				))}
			</div>
			{canRollback && (
				<DialogFooter>
					<Button
						variant="outline"
						className="h-[30px]"
						onClick={() => {
							onRollback(from, region.current);
						}}
					>
						Roll back to {shortId(from.id)}
					</Button>
				</DialogFooter>
			)}
		</DialogContent>
	);
}
