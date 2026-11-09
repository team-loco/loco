import { ArrowRightIcon, CheckIcon, ChevronDownIcon, CopyIcon, DownloadIcon } from "lucide-react";
import type { Deployment } from "@gen/loco/deployment/v1/deployment_pb";

import { Button } from "@/components/design/Button";
import { DialogContent, DialogTitle } from "@/components/design/Dialog";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuRadioGroup,
	DropdownMenuRadioItem,
	DropdownMenuTrigger,
} from "@/components/design/DropdownMenu";
import { useCopy } from "@/hooks/useCopy";
import { cn } from "@/lib/utils";

import { formatStarted, shortId } from "./format";
import { depTag, startedMs } from "./model";
import { diffLines, sideBySide, specText } from "./spec";

const COPIED_MS = 1500;
const JSON_TYPE = "application/json";

function optionLabel(dep: Deployment): string {
	return [shortId(dep.id), dep.region, depTag(dep), formatStarted(startedMs(dep))].join(" · ");
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
							{optionLabel(d)}
						</DropdownMenuRadioItem>
					))}
				</DropdownMenuRadioGroup>
			</DropdownMenuContent>
		</DropdownMenu>
	);
}

function download(dep: Deployment, text: string) {
	const url = URL.createObjectURL(new Blob([text], { type: JSON_TYPE }));
	const a = document.createElement("a");
	a.href = url;
	a.download = `deployment-${shortId(dep.id)}.json`;
	a.click();
	URL.revokeObjectURL(url);
}

function Single({ text }: { text: string }) {
	return (
		<>
			{text.split("\n").map((line, i) => (
				<div key={i} className="grid grid-cols-[44px_minmax(0,1fr)]">
					<span className="pr-3 text-right text-fg4 select-none">{i + 1}</span>
					<span className="pr-3 whitespace-pre">{line}</span>
				</div>
			))}
		</>
	);
}

function Compare({ from, to }: { from: string; to: string }) {
	const rows = sideBySide(diffLines(from, to).lines);
	const side = (cell: (typeof rows)[number]["left"], tone: "add" | "del") => {
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
				<span className={cn("overflow-hidden pr-3 text-ellipsis whitespace-pre", cls)}>{cell?.text ?? ""}</span>
			</>
		);
	};
	return (
		<>
			{rows.map((r, i) => (
				<div
					key={i}
					className="grid grid-cols-[40px_minmax(0,1fr)_40px_minmax(0,1fr)] [&>span:nth-child(3)]:border-l [&>span:nth-child(3)]:border-line"
				>
					{side(r.left, "del")}
					{side(r.right, "add")}
				</div>
			))}
		</>
	);
}

export function SpecDialogContent({
	deployments,
	deploymentId,
	compareId,
	onChange,
}: {
	deployments: Deployment[];
	deploymentId: string;
	compareId: string | null;
	onChange: (deploymentId: string, compareId: string | null) => void;
}) {
	const [copiedKey, copy] = useCopy(COPIED_MS);
	const a = deployments.find((d) => d.id === deploymentId) ?? deployments[0];
	if (a === undefined) return null;
	const b = compareId === null ? undefined : deployments.find((d) => d.id === compareId);
	const aText = specText(a);
	const diff = b === undefined ? undefined : diffLines(aText, specText(b));
	const others = deployments.filter((d) => d.id !== a.id);
	const firstOther = others[0];

	return (
		<DialogContent className={cn("max-h-[calc(100dvh-64px)]", b === undefined ? "w-[720px]" : "w-[1180px]")}>
			<div className="flex flex-wrap items-center gap-2.5 border-b border-line py-2.5 pr-12 pl-4">
				<DialogTitle className="text-[14px]">Deployment spec</DialogTitle>
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
						variant="link"
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
						<Button
							variant="link"
							onClick={() => {
								onChange(a.id, null);
							}}
						>
							Stop comparing
						</Button>
					</>
				)}
				<span className="flex-1" />
				<Button
					variant="ghost"
					size="icon-sm"
					aria-label="Download spec"
					onClick={() => {
						download(a, aText);
					}}
				>
					<DownloadIcon />
				</Button>
				<Button
					variant="ghost"
					size="icon-sm"
					aria-label={copiedKey === a.id ? "Copied" : "Copy spec"}
					onClick={() => {
						copy(a.id, aText);
					}}
				>
					{copiedKey === a.id ? <CheckIcon /> : <CopyIcon />}
				</Button>
			</div>
			<div className="min-h-0 overflow-auto bg-panel pt-2 pb-3 font-mono text-[12.5px] leading-5">
				{b === undefined ? <Single text={aText} /> : <Compare from={aText} to={specText(b)} />}
			</div>
		</DialogContent>
	);
}
