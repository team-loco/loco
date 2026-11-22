import { useTransport } from "@connectrpc/connect-query";
import type { Replica } from "@gen/loco/deployment/v1/deployment_pb";
import { ResourceStatus } from "@gen/loco/resource/v1/resource_pb";
import { useQueries } from "@tanstack/react-query";
import { ChartLineIcon, ScrollTextIcon } from "lucide-react";
import { Link } from "react-router";

import { Badge, type BadgeTone } from "@/components/design/Badge";
import { StatusGlyph, type Glyph } from "@/components/design/StatusGlyph";
import { Button } from "@/components/design/Button";
import { Skeleton } from "@/components/design/Skeleton";
import { metricsQueryOptions } from "@/hooks/useQueryMetrics";
import { formatMemory } from "@/lib/quantity";
import { agoLabel, maybeTsMs, shortDurationLabel } from "@/lib/time";
import { cn } from "@/lib/utils";

import { replicaState } from "./deployment-progress";
import { shortName } from "./MetricChart";
import { depTag, regionDeployment, type RegionView } from "./model";
import { MAX_REPLICA_LINES } from "./metric";
import { exitLabel, podUsage, regionReady, replicaLimits, restartCount, restartLabel } from "./replicas";

const USAGE_SPAN_MS = 5 * 60_000;
const USAGE_METRICS = ["k8s.pod.cpu_limit_utilization", "k8s.pod.memory.working_set_limit_utilization"] as const;
const HIGH_SHARE = 0.85;
const MILLI_PER_CORE = 1000;
const MI_PER_GI = 1024;
/** Regions with more replicas than this show one small cell per replica. */
const MANY_REPLICAS = 24;

const CELL_TONE: Record<BadgeTone, string> = {
	ok: "bg-ok-glyph",
	info: "bg-info",
	warn: "bg-warn",
	bad: "bg-bad-fg",
	neutral: "bg-line2",
	muted: "bg-line2",
	pill: "bg-line2",
};

const GLYPH: Record<BadgeTone, Glyph> = {
	ok: "ok",
	info: "spin",
	warn: "warn",
	bad: "err",
	neutral: "off",
	muted: "off",
	pill: "off",
};

/** A limit in whole units without trailing zeros. */
function limitUnits(value: number): string {
	return Number(value.toFixed(3)).toString();
}

/** Usage against its limit, as "0.18/1" for CPU and "0.31/2 Gi" or "96/256 Mi" for memory. */
function usageText(ratio: number | undefined, limit: number, kind: "cpu" | "memory"): { used: string; limit: string } {
	if (limit <= 0) return { used: "—", limit: "" };
	if (kind === "cpu") {
		const cores = limit / MILLI_PER_CORE;
		return { used: ratio === undefined ? "—" : (ratio * cores).toFixed(2), limit: `/${limitUnits(cores)}` };
	}
	const gi = limit >= MI_PER_GI;
	const used =
		ratio === undefined ? "—" : gi ? ((ratio * limit) / MI_PER_GI).toFixed(2) : Math.round(ratio * limit).toString();
	return { used, limit: `/${formatMemory(limit)}` };
}

function Usage({
	name,
	ratio,
	limit,
	kind,
}: {
	name: string;
	ratio: number | undefined;
	limit: number;
	kind: "cpu" | "memory";
}) {
	const text = usageText(ratio, limit, kind);
	return (
		<>
			<span className="font-sans text-[11px] text-fg3">{name}</span>{" "}
			<span className={cn("text-foreground", ratio !== undefined && ratio >= HIGH_SHARE && "text-warn-fg")}>
				{text.used}
				<span className="text-fg4">{text.limit}</span>
			</span>{" "}
		</>
	);
}

/** The replicas the cluster last reported for one region, with each pod's CPU and memory against its limits. */
export function ReplicaList({
	workspaceId,
	environmentId,
	resourceId,
	region,
	selected,
	onSelect,
	onHover,
	lineColor,
	regionPressed,
	onPickRegion,
	nowMs,
}: {
	workspaceId: string;
	environmentId: string;
	resourceId: string;
	region: RegionView;
	regionPressed: boolean;
	onPickRegion: () => void;
	selected: string | null;
	onSelect: (replica: string | null) => void;
	onHover: (replica: string | null) => void;
	lineColor: (replica: string) => string | undefined;
	nowMs: number;
}) {
	const transport = useTransport();
	const [cpu, memory] = useQueries({
		queries: USAGE_METRICS.map((metricName) =>
			metricsQueryOptions(transport, {
				workspaceId,
				environmentId,
				resourceIds: [resourceId],
				region: region.name,
				spanMs: USAGE_SPAN_MS,
				metricName,
				byPod: true,
			}),
		),
	});
	const cpuUsage = podUsage(cpu?.data ?? [], nowMs);
	const memoryUsage = podUsage(memory?.data ?? [], nowMs);
	const replicas = region.status?.replicas ?? [];
	const stale = region.status?.status === ResourceStatus.UNKNOWN;
	const reportedMs = maybeTsMs(region.status?.reportedAt);
	const { ready, desired, previous } = regionReady(region);
	const measuring = cpu?.isLoading === true || memory?.isLoading === true;
	const many = replicas.length > MANY_REPLICAS;
	const limits = new Map(replicas.map((r) => [r.name, replicaLimits(region, r)]));
	const cpuUsed = replicas.flatMap((r) => {
		const share = cpuUsage.get(r.name);
		const milli = limits.get(r.name)?.cpuMilli ?? 0;
		return share === undefined || milli <= 0 ? [] : [share * milli];
	});
	const average =
		replicas.length > MAX_REPLICA_LINES && cpuUsed.length > 0
			? `avg ${(cpuUsed.reduce((a, b) => a + b, 0) / cpuUsed.length / MILLI_PER_CORE).toFixed(2)} vCPU`
			: undefined;
	return (
		<section aria-label={`Replicas in ${region.name}`} className="flex min-w-0 flex-col gap-2">
			<h3 className="m-0 flex flex-wrap items-baseline gap-x-2 text-[12px] font-normal text-fg3">
				<button
					type="button"
					aria-pressed={regionPressed}
					onClick={onPickRegion}
					className="-ml-1.5 flex items-baseline gap-2 rounded-md border-0 bg-transparent px-1.5 py-0.5 text-left hover:bg-background aria-pressed:bg-background"
				>
					<span className="font-mono text-[12.5px] font-medium text-foreground">{region.name}</span>
					<span>
						{`${ready.toString()} of ${desired.toString()} ready`}
						{previous > 0 && `, ${previous.toString()} previous`}
					</span>
					{average !== undefined && <span className="font-mono">{average}</span>}
				</button>
				{stale && (
					<span>{reportedMs === undefined ? "Not reported yet" : `Last reported ${agoLabel(reportedMs, nowMs)}`}</span>
				)}
			</h3>
			{replicas.length === 0 && <p className="m-0 text-[12px] text-fg3">No replicas reported</p>}
			<ul
				className={cn(
					"m-0 list-none p-0",
					many ? "flex flex-wrap gap-1" : "flex flex-wrap gap-2",
					stale && "opacity-60",
				)}
			>
				{replicas.map((replica) => {
					const state = replicaState(replica.state);
					const label = many ? `${replica.name}: ${state.label}` : undefined;
					const color = lineColor(replica.name);
					const restarts = replica.restarts > 0 ? `, ${restartCount(replica)}` : "";
					const limit = limits.get(replica.name);
					return (
						<li key={replica.uid} className={cn("flex min-w-0", !many && "w-[172px] max-w-full")}>
							<button
								type="button"
								aria-pressed={selected === replica.name}
								aria-label={label}
								title={label}
								data-slot={many ? undefined : "replica-chip"}
								data-bad={state.tone === "bad" || undefined}
								onClick={() => {
									onSelect(selected === replica.name ? null : replica.name);
								}}
								onMouseEnter={() => {
									onHover(replica.name);
								}}
								onMouseLeave={() => {
									onHover(null);
								}}
								onFocus={() => {
									onHover(replica.name);
								}}
								onBlur={() => {
									onHover(null);
								}}
								className={
									many
										? cn(
												"size-3.5 rounded-[3px] border-0 p-0 aria-pressed:ring-2 aria-pressed:ring-ring aria-pressed:ring-offset-1",
												CELL_TONE[state.tone],
											)
										: "flex min-w-0 flex-1 flex-col gap-1 rounded-lg border border-line bg-background px-3 py-[9px] text-left hover:border-fg4 aria-pressed:border-foreground data-bad:border-warn/50 data-bad:aria-pressed:border-warn"
								}
							>
								{!many && (
									<>
										<span className="flex min-w-0 items-center gap-1.5 text-[12.5px]">
											<StatusGlyph glyph={GLYPH[state.tone]} className="size-3" />
											<span className="min-w-0 truncate font-mono">{shortName(replica.name)}</span>
											{color !== undefined && (
												<span
													aria-hidden
													data-slot="replica-key"
													className="ml-auto h-0.5 w-3 shrink-0 rounded-[1px]"
													style={{ background: color }}
												/>
											)}
										</span>
										{replica.state === "Serving" ? (
											<span className="sr-only">{state.label}</span>
										) : (
											<span className="mb-0.5 truncate text-[11.5px] text-warn-fg">{`${state.label}${restarts}`}</span>
										)}
										{measuring ? (
											<Skeleton className="h-3.5 w-28" />
										) : (
											<span className="grid grid-cols-[auto_auto] items-baseline justify-start gap-x-2 gap-y-px font-mono text-[12px] tabular-nums">
												<Usage name="CPU" ratio={cpuUsage.get(replica.name)} limit={limit?.cpuMilli ?? 0} kind="cpu" />
												<Usage
													name="Mem"
													ratio={memoryUsage.get(replica.name)}
													limit={limit?.memoryMi ?? 0}
													kind="memory"
												/>
											</span>
										)}
									</>
								)}
							</button>
						</li>
					);
				})}
			</ul>
		</section>
	);
}

/** Details of the replica the user picked: its state, uptime, restarts, version and links to its telemetry. */
export function ReplicaDetail({
	replica,
	region,
	nowMs,
	metricsHref,
	logsHref,
}: {
	replica: Replica;
	region: RegionView;
	nowMs: number;
	metricsHref: string | undefined;
	logsHref: string | undefined;
}) {
	const state = replicaState(replica.state);
	const startedMs = maybeTsMs(replica.startedAt);
	const deployment = regionDeployment(region, replica.deploymentId);
	return (
		<section
			aria-label={`Replica ${replica.name}`}
			className="flex flex-wrap items-center gap-x-5 gap-y-2 rounded-lg bg-background px-3.5 py-2.5 text-[12.5px]"
		>
			<span className="flex min-w-0 items-center gap-2">
				<span className="break-all font-mono text-[13px]">{replica.name}</span>
				<Badge size="sm" tone={state.tone}>
					{state.label}
				</Badge>
			</span>
			<span className="flex gap-1.5">
				<span className="text-fg3">Up</span>{" "}
				<span className="font-mono">{startedMs === undefined ? "—" : shortDurationLabel(nowMs - startedMs)}</span>
			</span>
			<span className="flex gap-1.5">
				<span className="text-fg3">Restarts</span>{" "}
				<span className={cn("font-mono", replica.restarts > 0 && "text-warn-fg")}>{restartLabel(replica, nowMs)}</span>
			</span>
			{replica.lastExit !== undefined && replica.lastRestart === undefined && (
				<span className="font-mono text-warn-fg [overflow-wrap:anywhere]">{exitLabel(replica.lastExit, nowMs)}</span>
			)}
			<span className="flex gap-1.5">
				<span className="text-fg3">Version</span>{" "}
				<span className="font-mono">{deployment === undefined ? "—" : depTag(deployment)}</span>
			</span>
			<span className="ml-auto flex gap-2">
				{metricsHref !== undefined && (
					<Button variant="outline" size="sm" nativeButton={false} render={<Link to={metricsHref} />}>
						<ChartLineIcon />
						Metrics
					</Button>
				)}
				{logsHref !== undefined && (
					<Button variant="outline" size="sm" nativeButton={false} render={<Link to={logsHref} />}>
						<ScrollTextIcon />
						Logs
					</Button>
				)}
			</span>
		</section>
	);
}
