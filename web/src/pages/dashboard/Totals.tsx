import { parseAsStringLiteral, useQueryState } from "nuqs";
import { useTraffic } from "@/hooks/useTraffic";
import {
	changeLabel,
	formatBytes,
	formatCompact,
	sumTotals,
	trafficSeries,
	trafficTotals,
	type TrafficField,
	type TrafficPoint,
} from "@/lib/traffic";
import { stepSeconds } from "@/lib/obs-time";
import { RANGE_MS, METRIC_RANGES } from "@/pages/resource/metric";
import { getErrorMessage } from "@/lib/error-handler";
import { ChevronDownIcon } from "lucide-react";
import type { ReactNode } from "react";
import { Link } from "react-router";

import { SoonTag } from "@/components/design/SoonTag";
import { Sparkline } from "@/components/design/Sparkline";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { pluralize } from "@/lib/format";
import { formatCpu, formatMemory } from "@/lib/quantity";
import { cn } from "@/lib/utils";

import type { Allocation } from "./allocation";

const SPEND = "Spend";
const TOP = 5;
const PERCENT = 100;

const CARD = "flex min-h-full min-w-0 flex-col gap-1 rounded-card bg-panel px-5 pt-[18px] pb-4";
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
			<span data-slot="stat-value" className={VALUE} aria-hidden="true">
				—
			</span>
		</div>
	);
}

const EASE = "ease-[cubic-bezier(0.2,0.8,0.2,1)] motion-reduce:transition-none";

interface BreakdownRow {
	id: string;
	name: string;
	amount: number;
	label: string;
	detail?: string;
}

function Breakdown({
	rows,
	total,
	format,
	hrefFor,
}: {
	rows: BreakdownRow[];
	total: number;
	format: (n: number) => string;
	hrefFor: (id: string) => string;
}) {
	const shown = rows.length > TOP + 1 ? rows.slice(0, TOP) : rows;
	const rest = rows.slice(shown.length);
	const restAmount = rest.reduce((n, r) => n + r.amount, 0);
	const max = Math.max(1, shown[0]?.amount ?? 0);
	return (
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
						{pluralize(rows.length, "resource")}
						<ChevronDownIcon
							className={cn(
								EASE,
								"size-3.5 transition-transform duration-300 group-focus-within/stat:rotate-180 group-hover/stat:rotate-180",
							)}
						/>
					</span>
				</div>
				<div
					aria-hidden="true"
					data-slot="allocation-strip"
					className="mb-1.5 flex h-2 gap-0.5 overflow-hidden rounded-[3px]"
				>
					{shown.map((r, i) => (
						<span
							key={r.id}
							className={cn(
								EASE,
								"block h-full min-w-0.5 origin-bottom scale-y-0 transition-transform duration-[380ms] group-focus-within/stat:scale-y-100 group-hover/stat:scale-y-100",
							)}
							style={{
								flex: `${r.amount.toString()} 1 0`,
								backgroundColor: `var(--allocation-${i.toString()})`,
								transitionDelay: `${(i * 30).toString()}ms`,
							}}
						/>
					))}
					{rest.length > 0 && (
						<span
							data-rest
							className="h-full min-w-0.5 bg-line2 dark:bg-panel2"
							style={{ flex: `${restAmount.toString()} 1 0` }}
						/>
					)}
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
									<span className="min-w-0 flex-1 truncate text-[13.5px] font-medium text-foreground" title={r.name}>
										{r.name}
									</span>
									<span className="flex shrink-0 items-baseline gap-2 font-mono whitespace-nowrap">
										<span className="text-[12.5px]">{r.label}</span>
										{r.detail !== undefined && <span className="text-[11.5px] text-fg3">{r.detail}</span>}
										<span className="w-8 text-right text-[11px] text-fg3">{share(r.amount, total)}</span>
									</span>
								</span>
								<span data-slot="allocation-bar" className="block h-[3px] overflow-hidden rounded-[2px] bg-panel2">
									<span
										className={cn(
											EASE,
											"block h-full origin-left scale-x-0 rounded-[2px] transition-transform duration-[520ms] group-focus-within/stat:scale-x-100 group-hover/stat:scale-x-100",
										)}
										style={{
											width: `${((r.amount / max) * PERCENT).toFixed(1)}%`,
											backgroundColor: `var(--allocation-${i.toString()})`,
										}}
									/>
								</span>
							</Link>
						</li>
					))}
					{rest.length > 0 && (
						<li className="flex items-center justify-between gap-2.5 py-2 text-[13.5px] text-fg2">
							{pluralize(rest.length, "more resource")}
							<span className="flex items-baseline gap-2 font-mono">
								<span className="text-[12.5px] text-foreground">{format(restAmount)}</span>
								<span className="w-8 text-right text-[11px] text-fg3">{share(restAmount, total)}</span>
							</span>
						</li>
					)}
				</ul>
			</div>
		</div>
	);
}

/** A stat card that lifts on hover or focus and unfolds a per-resource breakdown. */
function StatCard({
	label,
	ariaLabel,
	corner,
	children,
	breakdown,
	growLeft = false,
}: {
	label: string;
	ariaLabel: string;
	corner?: ReactNode;
	children: ReactNode;
	breakdown: ReactNode;
	growLeft?: boolean;
}) {
	const lifts = breakdown !== null;
	return (
		<div className="group/stat relative min-h-[178px]">
			<div
				role="group"
				aria-label={ariaLabel}
				className={cn(
					CARD,
					EASE,
					"absolute top-0 z-[1] w-full outline-none transition-[transform,box-shadow,background-color] duration-300",
					growLeft ? "right-0 origin-top-right" : "left-0 origin-top-left",
					lifts &&
						"group-focus-within/stat:z-40 group-focus-within/stat:w-[max(100%,340px)] group-focus-within/stat:-translate-y-1.5 group-focus-within/stat:scale-[1.025] group-focus-within/stat:bg-background group-focus-within/stat:shadow-overlay group-focus-within/stat:ring-1 group-focus-within/stat:ring-line group-hover/stat:z-40 group-hover/stat:w-[max(100%,340px)] group-hover/stat:-translate-y-1.5 group-hover/stat:scale-[1.025] group-hover/stat:bg-background group-hover/stat:shadow-overlay group-hover/stat:ring-1 group-hover/stat:ring-line",
				)}
			>
				<CardHead label={label}>{corner}</CardHead>
				{children}
				{breakdown}
			</div>
		</div>
	);
}

function AllocatedCard({ value, hrefFor }: { value: Allocation; hrefFor: (id: string) => string }) {
	return (
		<StatCard
			label="Allocated"
			growLeft
			ariaLabel={`Allocated: ${formatCpu(value.cpuMilli)}, ${formatMemory(value.memMi)} memory`}
			corner={<span className="font-mono text-[11.5px]">now</span>}
			breakdown={
				value.resources.length > 0 ? (
					<Breakdown
						rows={value.resources.map((r) => ({
							id: r.id,
							name: r.name,
							amount: r.cpuMilli,
							label: formatCpu(r.cpuMilli),
							detail: formatMemory(r.memMi),
						}))}
						total={value.cpuMilli}
						format={formatCpu}
						hrefFor={hrefFor}
					/>
				) : null
			}
		>
			<span data-slot="stat-value" className={VALUE}>
				{formatCpu(value.cpuMilli)}
			</span>
			<span className="truncate font-mono text-[11.5px] text-fg3" title={`${formatMemory(value.memMi)} memory`}>
				{formatMemory(value.memMi)} memory
			</span>
		</StatCard>
	);
}

interface TrafficCardSpec {
	label: string;
	field: TrafficField;
	format: (n: number) => string;
	tone: string;
	warnOnRise: boolean;
}

const TRAFFIC_CARDS: TrafficCardSpec[] = [
	{ label: "Requests", field: "requests", format: formatCompact, tone: "text-foreground", warnOnRise: false },
	{ label: "Errors", field: "errors", format: formatCompact, tone: "text-warn", warnOnRise: true },
	{ label: "Bandwidth out", field: "bytes", format: formatBytes, tone: "text-foreground", warnOnRise: false },
];

function TrafficCard({
	spec,
	current,
	prior,
	stepMs,
	range,
	state,
	names,
	hrefFor,
}: {
	spec: TrafficCardSpec;
	current: TrafficPoint[];
	prior: TrafficPoint[];
	stepMs: number;
	range: string;
	state: "loading" | "error" | "ready";
	names: Map<string, string>;
	hrefFor: (id: string) => string;
}) {
	const perResource = trafficTotals(current, spec.field, stepMs);
	const total = sumTotals(perResource);
	const change = changeLabel(total, sumTotals(trafficTotals(prior, spec.field, stepMs)));
	const shown = state === "loading" ? "…" : state === "error" || total === undefined ? "—" : spec.format(total);
	const rows = [...perResource.entries()]
		.filter(([, amount]) => amount > 0)
		.map(([id, amount]) => ({ id, name: names.get(id) ?? id, amount, label: spec.format(amount) }))
		.toSorted((a, b) => b.amount - a.amount || a.name.localeCompare(b.name));
	const comparison = `${change ?? "—"} vs prior ${range}`;
	return (
		<StatCard
			label={spec.label}
			ariaLabel={`${spec.label}: ${shown}, ${comparison}`}
			breakdown={
				state === "ready" && rows.length > 0 && total !== undefined ? (
					<Breakdown rows={rows} total={total} format={spec.format} hrefFor={hrefFor} />
				) : null
			}
		>
			<span data-slot="stat-value" className={VALUE}>
				{shown}
			</span>
			<span
				className={cn(
					"truncate font-mono text-[11.5px]",
					spec.warnOnRise && change?.startsWith("↑") === true ? "text-warn" : "text-fg3",
				)}
				title={comparison}
			>
				{state === "ready" ? comparison : " "}
			</span>
			<Sparkline
				values={state === "ready" ? trafficSeries(current, spec.field) : []}
				fill={spec.warnOnRise ? 0.12 : 0.08}
				className={cn("mt-2 h-[38px]", spec.tone)}
			/>
		</StatCard>
	);
}

const rangeParser = parseAsStringLiteral(METRIC_RANGES).withDefault("24h").withOptions({ history: "replace" });
export function Totals({
	value,
	hrefFor,
	workspaceId,
	environmentId,
	nowMs,
}: {
	value: Allocation;
	hrefFor: (id: string) => string;
	workspaceId: string;
	environmentId: string;
	nowMs: number;
}) {
	const [range, setRange] = useQueryState("range", rangeParser);
	const spanMs = RANGE_MS[range];
	const stepMs = stepSeconds(spanMs) * 1000;
	const scope = { workspaceId, environmentId, resourceIds: [], spanMs };
	const traffic = useTraffic(scope);
	const prior = useTraffic({ ...scope, endMs: Math.floor(nowMs / stepMs) * stepMs - spanMs });
	const state = traffic.isLoading ? "loading" : traffic.error !== null ? "error" : "ready";
	const names = new Map(value.resources.map((r) => [r.id, r.name]));
	return (
		<div className="flex flex-col gap-3">
			<div className="flex items-center justify-end gap-2">
				<ToggleGroup
					variant="segmented"
					aria-label="Time range"
					value={[range]}
					onValueChange={(values) => {
						const next = METRIC_RANGES.find((r) => r === values[0]);
						if (next !== undefined) void setRange(next);
					}}
				>
					{METRIC_RANGES.map((r) => (
						<ToggleGroupItem key={r} value={r}>
							{r}
						</ToggleGroupItem>
					))}
				</ToggleGroup>
			</div>
			<section
				aria-label="Totals"
				className="grid grid-cols-[repeat(auto-fit,minmax(min(180px,100%),1fr))] gap-2.5 sm:auto-rows-[minmax(178px,auto)]"
			>
				{TRAFFIC_CARDS.map((spec) => (
					<TrafficCard
						key={spec.label}
						spec={spec}
						current={traffic.points}
						prior={prior.points}
						stepMs={stepMs}
						range={range}
						state={state}
						names={names}
						hrefFor={hrefFor}
					/>
				))}
				<SoonCard label={SPEND} />
				<AllocatedCard value={value} hrefFor={hrefFor} />
			</section>
			{traffic.error !== null && (
				<p role="status" className="m-0 text-[12.5px] text-fg3">
					Traffic is unavailable: {getErrorMessage(traffic.error, "Failed to load traffic")}
				</p>
			)}
		</div>
	);
}
