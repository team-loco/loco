import { parseAsStringLiteral, useQueryState } from "nuqs";
import { useQueries } from "@tanstack/react-query";
import { createQueryOptions, useQuery, useTransport } from "@connectrpc/connect-query";
import { queryDeploymentBuckets } from "@gen/loco/deployment/v1/deployment-DeploymentService_connectquery";
import type { DeploymentBucket } from "@gen/loco/deployment/v1/deployment_pb";
import {
	getEnvironmentAllocation,
	listWorkspaceResources,
} from "@gen/loco/resource/v1/resource-ResourceService_connectquery";
import type { GetEnvironmentAllocationResponse } from "@gen/loco/resource/v1/resource_pb";
import { useTraffic } from "@/hooks/useTraffic";
import {
	changeLabel,
	formatBytes,
	formatCompact,
	sumTotals,
	windowSeries,
	trafficTotals,
	type TrafficWindow,
	type TrafficField,
	type TrafficPoint,
} from "@/lib/traffic";
import { stepSeconds } from "@/lib/obs-time";
import { DAY_MS, msToTimestamp } from "@/lib/time";
import { ChevronDownIcon } from "lucide-react";
import type { CSSProperties, ReactNode } from "react";
import { Link } from "react-router";

import { NavArrow } from "@/components/design/NavArrow";
import { Skeleton } from "@/components/design/Skeleton";
import { DeployMarkers } from "@/components/design/DeployMarkers";
import { PartialError } from "@/components/design/ErrorState";
import { SoonTag } from "@/components/design/SoonTag";
import { Sparkline } from "@/components/design/Sparkline";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { deployMarkers, MARKER_NAMES } from "@/lib/deployMarkers";
import { pluralize } from "@/lib/format";
import { bytesToMi, formatCpu, formatMemory } from "@/lib/quantity";
import { observabilityPath, resourcesPath } from "@/lib/routes";
import { cn } from "@/lib/utils";

import { trafficResourceName } from "./trafficNames";
import { useDeletedResourceNames } from "./useDeletedResourceNames";

const SPEND = "Spend";
const TOP = 5;
const PERCENT = 100;
const ALLOCATION_POLL_MS = 15_000;
const NAMES_PAGE = 100;

const RANGES = ["24h", "7d", "30d"] as const;
type Range = (typeof RANGES)[number];
const RANGE_DAYS: Record<Range, number> = { "24h": 1, "7d": 7, "30d": 30 };
const RANGE_BUCKETS: Record<Range, number> = { "24h": 12, "7d": 7, "30d": 10 };

function memoryLabel(mi: number): string {
	return `${formatMemory(mi)}B`;
}

const CARD = "flex min-h-full min-w-0 flex-col gap-1 rounded-card bg-panel px-5 pt-[18px] pb-4";
const VALUE = "font-mono text-[30px] leading-[1.25] font-medium tracking-[-0.03em] tabular-nums";

function share(part: number, total: number): string {
	if (total === 0) return "";
	const pct = (part / total) * PERCENT;
	return pct < 1 ? "<1%" : `${Math.round(pct).toString()}%`;
}

function CardHead({ label, children }: { label: string; children?: ReactNode }) {
	return (
		<span data-slot="stat-label" className="flex items-center justify-between gap-2 text-[13px] text-fg3">
			{label}
			{children}
		</span>
	);
}

function SoonCard({ label }: { label: string }) {
	return (
		<div role="group" className={cn(CARD, "text-fg4")} aria-label={`${label}: not available yet`}>
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
	href: string | undefined;
}

const ENTRY = cn(
	EASE,
	"group flex translate-y-1.5 flex-col gap-1.5 py-2 text-foreground no-underline opacity-0 transition-[opacity,transform] duration-[260ms] outline-none group-focus-within/stat:translate-y-0 group-focus-within/stat:opacity-100 group-hover/stat:translate-y-0 group-hover/stat:opacity-100 hover:no-underline focus-visible:ring-2 focus-visible:ring-ring",
);

function BreakdownEntry({
	href,
	style,
	children,
}: {
	href: string | undefined;
	style: CSSProperties;
	children: ReactNode;
}) {
	if (href === undefined)
		return (
			<div className={ENTRY} style={style}>
				{children}
			</div>
		);
	return (
		<Link to={href} className={cn(ENTRY, "group/nav")} style={style}>
			{children}
		</Link>
	);
}

/** Lists the largest of count rows; rows holds at least the first TOP + 1 of them, largest first. */
function Breakdown({
	rows,
	count = rows.length,
	total,
	format,
	moreHref,
}: {
	rows: BreakdownRow[];
	count?: number;
	total: number;
	format: (n: number) => string;
	moreHref: string;
}) {
	const shown = count > TOP + 1 ? rows.slice(0, TOP) : rows;
	const restCount = count - shown.length;
	const restAmount = Math.max(0, total - shown.reduce((n, r) => n + r.amount, 0));
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
						{pluralize(count, "resource")}
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
					{restCount > 0 && (
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
							<BreakdownEntry href={r.href} style={{ transitionDelay: `${(60 + i * 40).toString()}ms` }}>
								<span className="flex items-center justify-between gap-2.5">
									<span className="flex min-w-0 flex-1 items-center">
										<span className="min-w-0 truncate text-[13.5px] font-medium text-foreground" title={r.name}>
											{r.name}
										</span>
										{r.href !== undefined && <NavArrow />}
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
							</BreakdownEntry>
						</li>
					))}
					{restCount > 0 && (
						<li>
							<BreakdownEntry href={moreHref} style={{ transitionDelay: `${(60 + shown.length * 40).toString()}ms` }}>
								<span className="flex items-center justify-between gap-2.5 text-[13.5px] text-fg2">
									<span className="flex min-w-0 flex-1 items-center">
										{restCount.toString()} more
										<NavArrow />
									</span>
									<span className="flex items-baseline gap-2 font-mono">
										<span className="text-[12.5px] text-foreground">{format(restAmount)}</span>
										<span className="w-8 text-right text-[11px] text-fg3">{share(restAmount, total)}</span>
									</span>
								</span>
							</BreakdownEntry>
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
		<div data-slot="stat" className="group/stat relative min-h-[178px]">
			<div
				data-slot="stat-card"
				role="group"
				tabIndex={lifts ? 0 : undefined}
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

function AllocatedCard({
	value,
	failed,
	hrefFor,
	moreHref,
}: {
	value: GetEnvironmentAllocationResponse | undefined;
	failed: boolean;
	hrefFor: (id: string) => string;
	moreHref: string;
}) {
	if (value === undefined && failed)
		return (
			<StatCard label="Allocated" growLeft ariaLabel="Allocated: unavailable" breakdown={null}>
				<span data-slot="stat-value" className={VALUE}>
					—
				</span>
			</StatCard>
		);
	if (value === undefined)
		return (
			<StatCard label="Allocated" growLeft ariaLabel="Allocated: loading" breakdown={null}>
				<Skeleton className="my-1 h-[30px] w-24" />
				<Skeleton className="h-[14px] w-40" />
			</StatCard>
		);
	const cpuMilli = Number(value.cpuMillicores);
	const memory = memoryLabel(bytesToMi(value.memoryBytes));
	return (
		<StatCard
			label="Allocated"
			growLeft
			ariaLabel={`Allocated: ${formatCpu(cpuMilli)}, ${memory} memory`}
			corner={<span className="font-mono text-[11.5px]">now</span>}
			breakdown={
				value.resourceCount > 0 ? (
					<Breakdown
						rows={value.resources.map((r) => ({
							id: r.resourceId,
							name: r.name,
							amount: Number(r.cpuMillicores),
							label: formatCpu(Number(r.cpuMillicores)),
							detail: memoryLabel(bytesToMi(r.memoryBytes)),
							href: hrefFor(r.resourceId),
						}))}
						count={value.resourceCount}
						total={cpuMilli}
						format={formatCpu}
						moreHref={moreHref}
					/>
				) : null
			}
		>
			<span data-slot="stat-value" className={VALUE}>
				{formatCpu(cpuMilli)}
			</span>
			<span className="truncate font-mono text-[11.5px] text-fg3" title={`${memory} memory`}>
				{memory} memory
			</span>
		</StatCard>
	);
}

interface TrafficCardSpec {
	label: string;
	field: TrafficField;
	format: (n: number) => string;
	warnOnRise: boolean;
}

const TRAFFIC_CARDS: TrafficCardSpec[] = [
	{ label: "Requests", field: "requests", format: formatCompact, warnOnRise: false },
	{ label: "Errors", field: "errors", format: formatCompact, warnOnRise: true },
	{ label: "Bandwidth out", field: "bytes", format: formatBytes, warnOnRise: false },
];

function TrafficCard({
	spec,
	current,
	prior,
	stepMs,
	windows,
	range,
	state,
	names,
	deleted,
	hrefFor,
	moreHref,
	buckets,
	nowMs,
}: {
	spec: TrafficCardSpec;
	current: TrafficPoint[];
	prior: TrafficPoint[];
	stepMs: number;
	windows: { current: TrafficWindow; prior: TrafficWindow };
	range: string;
	state: "loading" | "error" | "ready";
	names: ReadonlyMap<string, string> | undefined;
	deleted: ReadonlyMap<string, string>;
	hrefFor: (id: string) => string;
	moreHref: string;
	buckets: readonly DeploymentBucket[];
	nowMs: number;
}) {
	const perResource = trafficTotals(current, spec.field, stepMs, windows.current);
	const total = sumTotals(perResource);
	const change = changeLabel(total, sumTotals(trafficTotals(prior, spec.field, stepMs, windows.prior)));
	const shown = state === "error" || total === undefined ? "—" : spec.format(total);
	const rows = [...perResource.entries()]
		.filter(([, amount]) => amount > 0)
		.map(([id, amount]) => {
			const { name, live } = trafficResourceName(id, names ?? new Map<string, string>(), deleted);
			return { id, name, amount, label: spec.format(amount), href: live ? hrefFor(id) : undefined };
		})
		.toSorted((a, b) => b.amount - a.amount || a.name.localeCompare(b.name));
	const comparison = change === undefined ? `No prior ${range} data to compare` : `${change} vs prior ${range}`;
	const warn = spec.warnOnRise && total !== undefined && total > 0;
	return (
		<StatCard
			label={spec.label}
			ariaLabel={state === "loading" ? `${spec.label}: loading` : `${spec.label}: ${shown}, ${comparison}`}
			breakdown={
				state === "ready" && names !== undefined && rows.length > 0 && total !== undefined ? (
					<Breakdown rows={rows} total={total} format={spec.format} moreHref={moreHref} />
				) : null
			}
		>
			{state === "loading" ? (
				<>
					<Skeleton className="my-1 h-[30px] w-24" />
					<Skeleton className="h-[14px] w-40" />
				</>
			) : (
				<>
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
				</>
			)}
			<div className="relative mt-2">
				<Sparkline
					values={state === "ready" ? windowSeries(current, spec.field, stepMs, windows.current) : []}
					fill={warn ? 0.12 : 0.08}
					className={cn("h-[38px]", warn ? "text-warn" : "text-foreground")}
				/>
				{state === "ready" && (
					<DeployMarkers markers={deployMarkers(buckets, windows.current.startMs, windows.current.endMs, nowMs)} />
				)}
			</div>
		</StatCard>
	);
}

const rangeParser = parseAsStringLiteral(RANGES).withDefault("24h").withOptions({ history: "replace" });
/** The live resources of the workspace among ids, by ID; undefined while they load. */
function useResourceNames(workspaceId: string, ids: readonly string[]): ReadonlyMap<string, string> | undefined {
	const transport = useTransport();
	const chunks = Array.from({ length: Math.ceil(ids.length / NAMES_PAGE) }, (_, i) =>
		ids.slice(i * NAMES_PAGE, (i + 1) * NAMES_PAGE),
	);
	const queries = useQueries({
		queries: chunks.map((resourceIds) =>
			createQueryOptions(listWorkspaceResources, { workspaceId, resourceIds, pageSize: NAMES_PAGE }, { transport }),
		),
	});
	if (queries.some((q) => q.data === undefined)) return undefined;
	return new Map(queries.flatMap((q) => (q.data?.resources ?? []).map((r) => [r.id, r.name] as const)));
}

export function Totals({
	hrefFor,
	orgId,
	workspaceId,
	environmentId,
	envName,
	nowMs,
}: {
	hrefFor: (id: string) => string;
	orgId: string;
	workspaceId: string;
	environmentId: string;
	envName: string;
	nowMs: number;
}) {
	const [range, setRange] = useQueryState("range", rangeParser);
	const spanMs = RANGE_DAYS[range] * DAY_MS;
	const stepMs = stepSeconds(spanMs) * 1000;
	const scope = { workspaceId, environmentId, resourceIds: [], spanMs };
	const traffic = useTraffic(scope);
	const priorEndMs = Math.floor(nowMs / stepMs) * stepMs - spanMs;
	const prior = useTraffic({ ...scope, endMs: priorEndMs });
	const windows = {
		current: { startMs: traffic.endMs - spanMs, endMs: traffic.endMs },
		prior: { startMs: priorEndMs - spanMs, endMs: priorEndMs },
	};
	const state = traffic.isLoading ? "loading" : traffic.error !== null ? "error" : "ready";
	const trafficIds = [...new Set([...traffic.points, ...prior.points].map((p) => p.resourceId))].toSorted();
	const names = useResourceNames(workspaceId, trafficIds);
	const gone = names !== undefined && trafficIds.some((id) => !names.has(id));
	const deleted = useDeletedResourceNames(orgId, workspaceId, gone);
	const buckets = useQuery(
		queryDeploymentBuckets,
		{
			workspaceId,
			environmentId,
			since: msToTimestamp(windows.current.startMs),
			until: msToTimestamp(windows.current.endMs),
			bucketCount: RANGE_BUCKETS[range],
			maxResourceNames: MARKER_NAMES,
		},
		{ enabled: state === "ready" },
	).data?.buckets;
	const allocation = useQuery(
		getEnvironmentAllocation,
		{ workspaceId, environmentId },
		{ refetchInterval: ALLOCATION_POLL_MS },
	);
	const metricsHref = observabilityPath(orgId, workspaceId, "metrics", { env: envName, range: spanMs });
	const sizeHref = `${resourcesPath(orgId, workspaceId)}?${new URLSearchParams({ env: envName, sort: "size.desc" }).toString()}`;
	return (
		<div className="flex flex-col gap-3">
			<div className="flex items-center justify-between gap-2">
				<p className="m-0 flex items-baseline gap-2 text-[12.5px]">
					<span className="font-medium text-foreground">Public traffic</span>
					<span className="text-fg3">Requests from the internet</span>
				</p>
				<ToggleGroup
					variant="segmented"
					aria-label="Time range"
					value={[range]}
					onValueChange={(values) => {
						const next = RANGES.find((r) => r === values[0]);
						if (next !== undefined) void setRange(next);
					}}
				>
					{RANGES.map((r) => (
						<ToggleGroupItem key={r} value={r}>
							{r}
						</ToggleGroupItem>
					))}
				</ToggleGroup>
			</div>
			{traffic.error !== null && (
				<PartialError
					title="Couldn’t load metrics"
					detail="Requests, errors and bandwidth are missing for this range."
					error={traffic.error}
					onRetry={traffic.refetch}
				/>
			)}
			{allocation.error !== null && (
				<PartialError
					title="Couldn’t load allocation"
					detail="The CPU and memory the environment is allocated are missing."
					error={allocation.error}
					onRetry={allocation.refetch}
				/>
			)}
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
						windows={windows}
						range={range}
						state={state}
						names={names}
						deleted={deleted}
						hrefFor={hrefFor}
						moreHref={metricsHref}
						buckets={buckets ?? []}
						nowMs={nowMs}
					/>
				))}
				<SoonCard label={SPEND} />
				<AllocatedCard
					value={allocation.data}
					failed={allocation.error !== null}
					hrefFor={hrefFor}
					moreHref={sizeHref}
				/>
			</section>
		</div>
	);
}
