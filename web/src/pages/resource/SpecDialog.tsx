import { useState, type CSSProperties } from "react";
import { ArrowRightIcon, CheckIcon, ChevronDownIcon, CopyIcon, DownloadIcon, TextWrapIcon, XIcon } from "lucide-react";
import { DeploymentStatus, type Deployment } from "@gen/loco/deployment/v1/deployment_pb";

import { Badge } from "@/components/design/Badge";
import { Button } from "@/components/design/Button";
import { Code } from "@/components/design/CodeBlock";
import { DialogClose, DialogContent, DialogTitle } from "@/components/design/Dialog";
import { ToggleChip } from "@/components/design/ToggleChip";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuRadioGroup,
	DropdownMenuRadioItem,
	DropdownMenuTrigger,
} from "@/components/design/DropdownMenu";
import { useCopy } from "@/hooks/useCopy";
import { cn, formatShortId } from "@/lib/utils";

import { depTag } from "./model";
import { changedOnly, diffLines, sideBySide, specText, type DiffLine, type SideBySideRow } from "./spec";

const COPIED_MS = 1500;
const YAML_TYPE = "application/yaml";

function optionLabel(dep: Deployment): string {
	return `${formatShortId(dep.id)} (${depTag(dep)})`;
}

function Picker({
	label,
	deployments,
	value,
	onChange,
}: {
	label: string;
	deployments: Deployment[];
	value: Deployment;
	onChange: (id: string) => void;
}) {
	return (
		<DropdownMenu>
			<DropdownMenuTrigger
				render={
					<Button
						variant="outline"
						size="sm"
						aria-label={label}
						className="max-w-[320px] gap-1.5 font-mono text-[12px]"
					/>
				}
			>
				<span className="truncate">{optionLabel(value)}</span>
				<ChevronDownIcon className="text-fg3" />
			</DropdownMenuTrigger>
			<DropdownMenuContent className="max-h-80 w-auto min-w-[320px]">
				<DropdownMenuRadioGroup value={value.id} onValueChange={onChange}>
					{deployments.map((d) => (
						<DropdownMenuRadioItem key={d.id} value={d.id} className="font-mono text-[12px]">
							<span className="flex-1">{optionLabel(d)}</span>
							{(d.status === DeploymentStatus.RUNNING || d.status === DeploymentStatus.DEGRADED) && (
								<Badge tone="pill">running</Badge>
							)}
						</DropdownMenuRadioItem>
					))}
				</DropdownMenuRadioGroup>
			</DropdownMenuContent>
		</DropdownMenu>
	);
}

function fileName(dep: Deployment): string {
	return `deployment-${formatShortId(dep.id)}.yaml`;
}

function download(dep: Deployment, text: string) {
	const url = URL.createObjectURL(new Blob([text], { type: YAML_TYPE }));
	const a = document.createElement("a");
	a.href = url;
	a.download = fileName(dep);
	a.click();
	URL.revokeObjectURL(url);
}

function Single({ text, wrap }: { text: string; wrap: boolean }) {
	const digits = text.split("\n").length.toString().length;
	return (
		<pre className="m-0 text-(--sh-identifier)" style={{ "--code-digits": digits } as CSSProperties}>
			<Code
				code={text}
				language="yaml"
				className={cn("code-lines [&>.sh__line:nth-child(-n+2)]:pr-[76px]", wrap && "code-wrap")}
			/>
		</pre>
	);
}

function Compare({ lines, diffOnly, wrap }: { lines: DiffLine[]; diffOnly: boolean; wrap: boolean }) {
	const all = sideBySide(lines);
	const rows = diffOnly ? changedOnly(all) : all;
	const side = (cell: SideBySideRow["left"], tone: "add" | "del") => {
		const cls =
			cell === null
				? "bg-panel2"
				: cell.changed
					? tone === "add"
						? "bg-ok-bg text-ok-fg"
						: "bg-bad-bg text-bad-fg"
					: "";
		return (
			<>
				<span className={cn("pr-3 text-right text-fg4 select-none", cls)}>{cell?.n ?? ""}</span>
				<span className={cn("pr-3", wrap ? "[overflow-wrap:anywhere] whitespace-pre-wrap" : "whitespace-pre", cls)}>
					{cell?.text ?? ""}
				</span>
			</>
		);
	};
	return (
		<div
			className={cn(
				"grid [&>div:nth-child(-n+2)>span:last-child]:pr-[76px]",
				wrap
					? "grid-cols-[40px_minmax(0,1fr)_40px_minmax(0,1fr)]"
					: "w-max min-w-full grid-cols-[40px_max-content_40px_max-content]",
			)}
		>
			{rows.map((r, i) => (
				<div key={i} className="contents [&>span:nth-child(3)]:border-l [&>span:nth-child(3)]:border-line">
					{"skipped" in r ? (
						<span
							data-slot="skipped-rows"
							title={`${r.skipped.toString()} unchanged ${r.skipped === 1 ? "line" : "lines"}`}
							className="col-span-4 h-2 bg-[repeating-linear-gradient(-45deg,transparent_0_4px,color-mix(in_oklch,var(--fg)_7%,transparent)_4px_5px)]"
						/>
					) : (
						<>
							{side(r.left, "del")}
							{side(r.right, "add")}
						</>
					)}
				</div>
			))}
		</div>
	);
}

export function SpecDialogContent({
	resourceName,
	deployments,
	deploymentId,
	compareId,
	onChange,
}: {
	resourceName: string;
	deployments: Deployment[];
	deploymentId: string;
	compareId: string | null;
	onChange: (deploymentId: string, compareId: string | null) => void;
}) {
	const [copiedKey, copy] = useCopy(COPIED_MS);
	const [diffOnly, setDiffOnly] = useState(false);
	const [wrap, setWrap] = useState(true);
	const a = deployments.find((d) => d.id === deploymentId) ?? deployments[0];
	if (a === undefined) return null;
	const b = compareId === null ? undefined : deployments.find((d) => d.id === compareId);
	const aText = specText(a);
	const diff = b === undefined ? undefined : diffLines(aText, specText(b));
	const others = deployments.filter((d) => d.id !== a.id);
	const firstOther = others[0];

	return (
		<DialogContent top showCloseButton={false} className={cn(b === undefined ? "w-[720px]" : "w-[1180px]")}>
			<div className="flex flex-wrap items-center gap-2.5 border-b border-line py-2.5 pr-4 pl-4">
				<DialogTitle className="font-mono text-[15px]">
					{resourceName}
					<span className="sr-only"> deployment spec</span>
				</DialogTitle>
				<Picker
					label="Deployment"
					deployments={deployments}
					value={a}
					onChange={(id) => {
						onChange(id, compareId === id ? null : compareId);
					}}
				/>
				{b === undefined && firstOther !== undefined && (
					<Button
						variant="plain-link"
						onClick={() => {
							onChange(a.id, firstOther.id);
						}}
					>
						Compare with…
					</Button>
				)}
				{b !== undefined && diff !== undefined && (
					<>
						<ArrowRightIcon aria-hidden className="size-3.5 text-fg3" />
						<Picker
							label="Compare with"
							deployments={others}
							value={b}
							onChange={(id) => {
								onChange(a.id, id);
							}}
						/>
						<span className="font-mono text-[12px]">
							<span className="text-ok-fg">+{diff.add}</span> <span className="text-bad-fg">−{diff.del}</span>
						</span>
						<ToggleChip
							variant="button"
							className="h-7 px-2.5 text-[12.5px]"
							pressed={diffOnly}
							onPressedChange={setDiffOnly}
						>
							Diff only
						</ToggleChip>
						<Button
							variant="plain-link"
							onClick={() => {
								onChange(a.id, null);
							}}
						>
							Stop comparing
						</Button>
					</>
				)}
				<div className="ml-auto flex items-center gap-1">
					<Button
						variant="ghost"
						size="icon-xs"
						aria-label="Download spec"
						title={`Download ${fileName(a)}`}
						onClick={() => {
							download(a, aText);
						}}
					>
						<DownloadIcon />
					</Button>
					<DialogClose render={<Button variant="ghost" size="icon-xs" aria-label="Close" title="Close" />}>
						<XIcon />
					</DialogClose>
				</div>
			</div>
			<div className="relative flex min-h-0 flex-1 flex-col bg-panel">
				<ToggleChip
					variant="button"
					aria-label="Wrap lines"
					title="Wrap lines"
					className="absolute top-1.5 right-[38px] z-10 size-6 min-w-0 border-0 bg-panel px-0 text-fg3 hover:text-foreground aria-pressed:bg-panel2"
					pressed={wrap}
					onPressedChange={setWrap}
				>
					<TextWrapIcon />
				</ToggleChip>
				<Button
					variant="ghost"
					size="icon-xs"
					className="absolute top-1.5 right-1.5 z-10 bg-panel text-fg3"
					aria-label={copiedKey === a.id ? "Copied" : "Copy spec"}
					title="Copy"
					onClick={() => {
						copy(a.id, aText);
					}}
				>
					{copiedKey === a.id ? <CheckIcon /> : <CopyIcon />}
				</Button>
				<div data-slot="spec-body" className="min-h-0 overflow-auto pt-2 pb-3 font-mono text-[12.5px] leading-5">
					{b === undefined || diff === undefined ? (
						<Single text={aText} wrap={wrap} />
					) : (
						<Compare lines={diff.lines} diffOnly={diffOnly} wrap={wrap} />
					)}
				</div>
			</div>
		</DialogContent>
	);
}
