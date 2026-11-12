import { ChevronDownIcon } from "lucide-react";
import type { ReactNode } from "react";
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
const SPEND = "Spend";
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

const EASE = "ease-[cubic-bezier(0.2,0.8,0.2,1)] motion-reduce:transition-none";
const SEG_FADE = 0.12;
const MIN_OPACITY = 0.3;

function AllocatedCard({ value, hrefFor }: { value: Allocation; hrefFor: (id: string) => string }) {
	const shown = value.resources.length > TOP + 1 ? value.resources.slice(0, TOP) : value.resources;
	const rest = value.resources.slice(shown.length);
	const restCpu = rest.reduce((n, r) => n + r.cpuMilli, 0);
	const max = shown[0]?.cpuMilli ?? 0;
	const hasBreakdown = value.resources.length > 0;

	return (
		<div className="group/stat relative min-h-[150px]">
			<div
				role="group"
				aria-label={`Allocated: ${formatCpu(value.cpuMilli)}, ${formatMemory(value.memMi)} memory`}
				className={cn(
					CARD,
					EASE,
					"absolute top-0 right-0 z-[1] w-full origin-top-right outline-none transition-[transform,box-shadow,background-color] duration-300",
					hasBreakdown &&
						"group-focus-within/stat:z-40 group-focus-within/stat:w-[max(100%,340px)] group-focus-within/stat:-translate-y-1.5 group-focus-within/stat:scale-[1.025] group-focus-within/stat:bg-background group-focus-within/stat:shadow-overlay group-focus-within/stat:ring-1 group-focus-within/stat:ring-line group-hover/stat:z-40 group-hover/stat:w-[max(100%,340px)] group-hover/stat:-translate-y-1.5 group-hover/stat:scale-[1.025] group-hover/stat:bg-background group-hover/stat:shadow-overlay group-hover/stat:ring-1 group-hover/stat:ring-line",
				)}
			>
				<CardHead label="Allocated">
					<span className="font-mono text-[11.5px]">now</span>
				</CardHead>
				<span className={VALUE}>{formatCpu(value.cpuMilli)}</span>
				<span className="font-mono text-[11.5px] text-fg3">{formatMemory(value.memMi)} memory</span>
				{hasBreakdown && (
					<div
						className={cn(
							EASE,
							"grid grid-rows-[0fr] transition-[grid-template-rows] duration-[340ms] group-focus-within/stat:grid-rows-[1fr] group-hover/stat:grid-rows-[1fr]",
						)}
					>
						<div className="min-h-0 overflow-hidden">
							<div className="mt-3 flex items-center justify-between gap-2 border-t border-line pt-3.5 pb-2 text-[12px] text-fg3">
								By resource
								<span className="flex items-center gap-1.5 font-mono text-[11px]">
									{pluralize(value.resources.length, "resource")}
									<ChevronDownIcon
										className={cn(
											EASE,
											"size-3.5 transition-transform duration-300 group-focus-within/stat:rotate-180 group-hover/stat:rotate-180",
										)}
									/>
								</span>
							</div>
							<div aria-hidden="true" className="mb-1.5 flex h-2 gap-0.5 overflow-hidden rounded-[3px]">
								{value.resources.map((r, i) => (
									<span
										key={r.id}
										className={cn(
											EASE,
											"block h-full min-w-0.5 origin-bottom scale-y-0 bg-foreground transition-transform duration-[380ms] group-focus-within/stat:scale-y-100 group-hover/stat:scale-y-100",
										)}
										style={{
											flex: `${r.cpuMilli.toString()} 1 0`,
											opacity: Math.max(MIN_OPACITY, 1 - i * SEG_FADE),
											transitionDelay: `${(i * 30).toString()}ms`,
										}}
									/>
								))}
							</div>
							<ul className="m-0 flex list-none flex-col p-0">
								{shown.map((r, i) => (
									<li key={r.id}>
										<Link
											to={hrefFor(r.id)}
											className={cn(
												EASE,
												"group flex translate-y-1.5 flex-col gap-1.5 py-2 text-foreground no-underline opacity-0 transition-[opacity,transform] duration-[260ms] outline-none group-focus-within/stat:translate-y-0 group-focus-within/stat:opacity-100 group-hover/stat:translate-y-0 group-hover/stat:opacity-100 hover:no-underline focus-visible:ring-2 focus-visible:ring-ring",
											)}
											style={{ transitionDelay: `${(60 + i * 40).toString()}ms` }}
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
													<span className="w-8 text-right text-[11px] text-fg3">
														{share(r.cpuMilli, value.cpuMilli)}
													</span>
												</span>
											</span>
											<span className="block h-[3px] overflow-hidden rounded-[2px] bg-panel2">
												<span
													className={cn(
														EASE,
														"block h-full origin-left scale-x-0 rounded-[2px] bg-foreground transition-transform duration-[520ms] group-focus-within/stat:scale-x-100 group-hover/stat:scale-x-100",
													)}
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
						</div>
					</div>
				)}
			</div>
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
				className="grid grid-cols-[repeat(auto-fit,minmax(min(180px,100%),1fr))] items-start gap-2.5"
			>
				{TRAFFIC.map((label) => (
					<SoonCard key={label} label={label} />
				))}
				<SoonCard label={SPEND} />
				<AllocatedCard value={value} hrefFor={hrefFor} />
			</section>
		</div>
	);
}
