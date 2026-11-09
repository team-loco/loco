import { ChevronDownIcon } from "lucide-react";
import { useId, useState, type ReactNode } from "react";
import { Link } from "react-router";

import { SoonTag } from "@/components/design/SoonTag";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { pluralize } from "@/lib/format";
import { formatCpu, formatMemory } from "@/lib/quantity";
import { cn } from "@/lib/utils";

import type { Allocation } from "./allocation";

const RANGES = ["24h", "7d", "30d"];
const DEFAULT_RANGE = "24h";
const TRAFFIC = ["Requests", "Errors", "Bandwidth out"];
const TOP = 5;
const PERCENT = 100;

const CARD = "flex min-w-0 sm:min-h-[150px] flex-col gap-1 rounded-card bg-panel px-5 pt-[18px] pb-4";
const VALUE = "font-mono text-[30px] leading-[1.25] font-medium tracking-[-0.03em] tabular-nums";

function share(part: number, total: number): string {
	if (total === 0) return "";
	const pct = (part / total) * PERCENT;
	return pct < 1 ? "<1%" : `${Math.round(pct).toString()}%`;
}

function CardHead({ label, children }: { label: string; children?: ReactNode }) {
	return (
		<span className="flex items-center justify-between gap-2 text-[13px] text-fg3">
			{label}
			{children}
		</span>
	);
}

function SoonCard({ label }: { label: string }) {
	return (
		<div className={cn(CARD, "text-fg4")} aria-label={`${label}: not available yet`}>
			<CardHead label={label}>
				<SoonTag />
			</CardHead>
			<span className={VALUE} aria-hidden="true">
				—
			</span>
		</div>
	);
}

function AllocatedCard({ value, hrefFor }: { value: Allocation; hrefFor: (id: string) => string }) {
	const [open, setOpen] = useState(false);
	const listId = useId();
	const shown = value.resources.length > TOP + 1 ? value.resources.slice(0, TOP) : value.resources;
	const rest = value.resources.slice(shown.length);
	const restCpu = rest.reduce((n, r) => n + r.cpuMilli, 0);
	const max = shown[0]?.cpuMilli ?? 0;

	return (
		<div className={CARD}>
			<CardHead label="Allocated">
				<span className="font-mono text-[11.5px]">now</span>
			</CardHead>
			<span className={VALUE}>{formatCpu(value.cpuMilli)}</span>
			<span className="font-mono text-[11.5px] text-fg3">{formatMemory(value.memMi)} memory</span>
			{value.resources.length > 0 && (
				<>
					<button
						type="button"
						aria-expanded={open}
						aria-controls={listId}
						className="mt-auto flex items-center justify-between gap-2 rounded-sm border-t border-line pt-3 text-[12px] text-fg3 outline-none hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring"
						onClick={() => {
							setOpen(!open);
						}}
					>
						By resource
						<span className="flex items-center gap-1.5 font-mono text-[11px]">
							{pluralize(value.resources.length, "resource")}
							<ChevronDownIcon className={cn("size-3.5 transition-transform", open && "rotate-180")} />
						</span>
					</button>
					<ul id={listId} hidden={!open} className="m-0 mt-1 flex list-none flex-col p-0">
						{shown.map((r) => (
							<li key={r.id}>
								<Link
									to={hrefFor(r.id)}
									className="group flex flex-col gap-1.5 py-2 text-foreground no-underline outline-none hover:no-underline focus-visible:ring-2 focus-visible:ring-ring"
								>
									<span className="flex items-center justify-between gap-2.5">
										<span
											className="min-w-0 flex-1 truncate text-[13.5px] font-medium underline-offset-3 group-hover:underline"
											title={r.name}
										>
											{r.name}
										</span>
										<span className="flex shrink-0 items-baseline gap-2 font-mono whitespace-nowrap">
											<span className="text-[12.5px]">{formatCpu(r.cpuMilli)}</span>
											<span className="text-[11.5px] text-fg3">{formatMemory(r.memMi)}</span>
											<span className="w-8 text-right text-[11px] text-fg3">{share(r.cpuMilli, value.cpuMilli)}</span>
										</span>
									</span>
									<span className="block h-[3px] overflow-hidden rounded-[2px] bg-panel2">
										<span
											className="block h-full rounded-[2px] bg-foreground"
											style={{ width: `${((r.cpuMilli / max) * PERCENT).toFixed(1)}%` }}
										/>
									</span>
								</Link>
							</li>
						))}
						{rest.length > 0 && (
							<li className="flex items-center justify-between gap-2.5 py-2 text-[13.5px] text-fg2">
								{pluralize(rest.length, "more resource")}
								<span className="flex items-baseline gap-2 font-mono">
									<span className="text-[12.5px] text-foreground">{formatCpu(restCpu)}</span>
									<span className="w-8 text-right text-[11px] text-fg3">{share(restCpu, value.cpuMilli)}</span>
								</span>
							</li>
						)}
					</ul>
				</>
			)}
		</div>
	);
}

export function Totals({ value, hrefFor }: { value: Allocation; hrefFor: (id: string) => string }) {
	return (
		<div className="flex flex-col gap-3">
			<div className="flex items-center justify-end gap-2">
				<SoonTag />
				<ToggleGroup variant="segmented" aria-label="Time range" disabled value={[DEFAULT_RANGE]}>
					{RANGES.map((r) => (
						<ToggleGroupItem key={r} value={r}>
							{r}
						</ToggleGroupItem>
					))}
				</ToggleGroup>
			</div>
			<section
				aria-label="Totals"
				className="grid grid-cols-[repeat(auto-fit,minmax(min(200px,100%),1fr))] items-start gap-2.5"
			>
				{TRAFFIC.map((label) => (
					<SoonCard key={label} label={label} />
				))}
				<AllocatedCard value={value} hrefFor={hrefFor} />
			</section>
		</div>
	);
}
