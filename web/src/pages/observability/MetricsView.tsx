import { useTimePreferences } from "@/lib/use-time-preferences";
import { timestampFromMs } from "@bufbuild/protobuf/wkt";
import { createQueryOptions, useTransport } from "@connectrpc/connect-query";
import { keepPreviousData, useQueries, useQuery } from "@tanstack/react-query";
import { functionalUpdate } from "@tanstack/react-table";
import type { Deployment } from "@gen/loco/deployment/v1/deployment_pb";
import { listDeployments } from "@gen/loco/deployment/v1/deployment-DeploymentService_connectquery";
import { queryMetrics } from "@gen/loco/observability/v1/observability-ObservabilityService_connectquery";
import { BellIcon, PlusIcon, RocketIcon, ScrollTextIcon, WaypointsIcon } from "lucide-react";
import { useQueryState } from "nuqs";
import { useState } from "react";
import { Link, useNavigate } from "react-router";

import { Button } from "@/components/design/Button";
import { CellText, DataTable, useDataTable, type DataColumn } from "@/components/design/DataTable";
import { ErrorState } from "@/components/design/ErrorState";
import { Section } from "@/components/design/Page";
import { Skeleton } from "@/components/design/Skeleton";
import { SoonTag } from "@/components/design/SoonTag";
import { useEnvironments } from "@/hooks/useEnvironment";
import { useNow } from "@/hooks/useNow";
import { currentDeploymentOptions } from "@/hooks/useWorkspaceDeployments";
import { workspaceDeploymentsOptions } from "@/hooks/workspaceDeployments";
import { metricsQueryOptions } from "@/hooks/useQueryMetrics";
import { formatPercent } from "@/lib/compute-chart";
import { getServiceSpec, regionTarget } from "@/lib/deployment-utils";
import { limitAt, limitSteps, startedIn, type Sizing } from "@/lib/limit-history";
import { formatCpu, formatMemory, parseCpuMilli, parseMemMi } from "@/lib/quantity";
import { isUnimplemented } from "@/lib/error-handler";
import { errorShare, TRAFFIC_METRICS, trafficPoints, trafficValue, type TrafficPoint } from "@/lib/traffic";
import { sortParser } from "@/lib/sort-parser";
import { HOUR_MS, MINUTE_MS, aroundWindow, msParser, resolveWindow, windowSpan } from "@/lib/obs-time";
import { newResourcePath, resourcePath } from "@/lib/routes";
import { formatHourMinute, readyMs, tsMs } from "@/lib/time";
import { cn, formatShortId } from "@/lib/utils";

import { filteredResources, useObs, type ObsResource } from "./context";
import { MetricChart, type ChartMarker, type ChartSeries, type PinAction } from "./MetricChart";
import { coversWholeRange, ObsHero, WidenRangeButton } from "./ObsEmpty";
import { liveTokens, narrow, type Token } from "./query";
import { QueryBar } from "./QueryBar";
import { ViewHeader } from "./ViewHeader";

interface MetricDef {
	key: "cpu" | "mem";
	metric: string;
	title: string;
	format: (value: number) => string;
	limit: (target: Sizing) => number;
}

const CPU: MetricDef = {
	key: "cpu",
	metric: "k8s.pod.cpu_limit_utilization",
	title: "CPU",
	format: formatCpu,
	limit: (target) => parseCpuMilli(target.cpu ?? ""),
};
const MEMORY: MetricDef = {
	key: "mem",
	metric: "k8s.pod.memory.working_set_limit_utilization",
	title: "Memory",
	format: formatMemory,
	limit: (target) => parseMemMi(target.memory ?? ""),
};

const [REQUESTS, ERRORS, , LATENCY] = TRAFFIC_METRICS;
const TRAFFIC_CARDS = [
	{
		key: "req",
		title: "Requests",
		column: "Requests",
		unit: "req/s",
		axisUnit: " req/s",
		value: (p: TrafficPoint) => p.requests,
		format: (v: number) => trafficValue(v, "/s"),
	},
	{
		key: "err",
		title: "5xx responses",
		column: "5xx",
		unit: "%",
		axisUnit: "%",
		value: errorShare,
		format: (v: number) => `${v.toFixed(2)}%`,
	},
	{
		key: "lat",
		title: "Latency, p95",
		column: "p95",
		unit: "ms",
		axisUnit: " ms",
		value: (p: TrafficPoint) => p.latency,
		format: (v: number) => trafficValue(v, " ms"),
	},
] as const;
type TrafficKey = (typeof TRAFFIC_CARDS)[number]["key"];
const PERCENT = 100;
const NOW_TICK_MS = 60_000;
const CHART_HEIGHT = 190;
const TRAFFIC_HEIGHT = 170;
// A deployment shows where it became ready, which can be this long after it was created.
const DEPLOY_LEAD_MS = 24 * HOUR_MS;
// Enough of the deployments created before the window to find the one running at its start.
const EARLIER_DEPLOYS = 5;
const WARN_PERCENT = 85;
// A click this close to a deploy line, as a share of the range, offers that deploy.
const DEPLOY_PICK_SHARE = 0.02;

const SORT_KEYS = ["resource", "region", "replicas", "cpu", "mem"] as const;
const metricSortParser = sortParser(SORT_KEYS, {
	id: "cpu",
	desc: true,
}).withOptions({ history: "replace" });

interface DeployMarker extends ChartMarker {
	tag: string;
	resourceId: string;
	resourceName: string;
	region: string;
}

interface PlacementSizing {
	deployments: Deployment[];
	target: Sizing | undefined;
}

interface Placement {
	key: string;
	resource: ObsResource;
	region: string;
	dashed: boolean;
	ready: number | null;
	desired: number | null;
	deploymentId: string;
}

function placements(resources: ObsResource[], environmentId: string, regionTokens: Token[]): Placement[] {
	const out: Placement[] = [];
	for (const resource of resources) {
		const status = resource.raw.environmentStatuses.find((s) => s.environmentId === environmentId);
		const regions =
			status !== undefined && status.regions.length > 0 ? status.regions.map((r) => r.region) : resource.regions;
		const kept = narrow(regions.toSorted(), regionTokens) ?? regions.toSorted();
		kept.forEach((region, i) => {
			const rs = status?.regions.find((r) => r.region === region);
			out.push({
				key: `${resource.id}|${region}`,
				resource,
				region,
				dashed: i > 0,
				ready: rs?.readyReplicas ?? null,
				desired: rs?.desiredReplicas ?? null,
				deploymentId: rs?.deploymentId ?? "",
			});
		});
	}
	return out;
}

function useComputeSeries(
	def: MetricDef,
	rows: Placement[],
	sizing: Map<string, PlacementSizing>,
	spanMs: number,
	endMs: number | undefined,
) {
	const transport = useTransport();
	const { workspaceId } = useObs();
	const { active: environment } = useEnvironments();
	const environmentId = environment?.id ?? "";
	const regions = [...new Set(rows.map((p) => p.region))];
	const results = useQueries({
		queries: regions.map((region) =>
			metricsQueryOptions(transport, {
				workspaceId,
				environmentId,
				resourceIds: [...new Set(rows.filter((p) => p.region === region).map((p) => p.resource.id))],
				spanMs,
				endMs,
				metricName: def.metric,
				aggregation: "max",
				region,
			}),
		),
	});
	const series: ChartSeries[] = [];
	const latest = new Map<string, { v: number; limit: number }>();
	const multi = new Set(rows.filter((p) => p.dashed).map((p) => p.resource.id));
	regions.forEach((region, i) => {
		const peaks = new Map<string, Map<number, number>>();
		for (const s of results[i]?.data ?? []) {
			const pts = peaks.get(s.resourceId) ?? new Map<number, number>();
			for (const p of s.points) {
				const t = tsMs(p.timestamp);
				pts.set(t, Math.max(pts.get(t) ?? 0, p.value * PERCENT));
			}
			peaks.set(s.resourceId, pts);
		}
		for (const [id, pts] of peaks) {
			const row = rows.find((p) => p.resource.id === id && p.region === region);
			if (row === undefined) continue;
			const placement = sizing.get(row.key);
			const limits =
				placement === undefined ? [] : limitSteps(placement.deployments, region, def.limit, placement.target);
			if (limits.length === 0) continue;
			const points = [...pts].map(([t, v]) => ({ t, v })).toSorted((a, b) => a.t - b.t);
			series.push({
				key: row.key,
				name: multi.has(id) ? `${row.resource.raw.name} ${region}` : row.resource.raw.name,
				color: row.resource.color,
				dashed: row.dashed,
				limits,
				points,
			});
			const last = points.at(-1);
			const limit = last === undefined ? undefined : limitAt(limits, last.t);
			if (last !== undefined && limit !== undefined) latest.set(row.key, { v: (last.v / PERCENT) * limit, limit });
		}
	});
	return {
		series,
		latest,
		isLoading: results.some((r) => r.isLoading),
		error: results.find((r) => r.error !== null)?.error ?? null,
		refetch: async () => await Promise.all(results.map(async (r) => await r.refetch())),
	};
}

/** Requests per second, the share of 5xx responses and p95 latency of each placement, from the gateway. */
function useTrafficSeries(rows: Placement[], spanMs: number, endMs: number | undefined) {
	const transport = useTransport();
	const { workspaceId } = useObs();
	const { active: environment } = useEnvironments();
	const environmentId = environment?.id ?? "";
	const regions = [...new Set(rows.map((p) => p.region))];
	const metrics = [REQUESTS, ERRORS, LATENCY];
	const results = useQueries({
		queries: regions.flatMap((region) =>
			metrics.map((metric) =>
				metricsQueryOptions(transport, {
					workspaceId,
					environmentId,
					resourceIds: [...new Set(rows.filter((p) => p.region === region).map((p) => p.resource.id))],
					spanMs,
					endMs,
					region,
					...metric,
				}),
			),
		),
	});
	const multi = new Set(rows.filter((p) => p.dashed).map((p) => p.resource.id));
	const series: Record<TrafficKey, ChartSeries[]> = { req: [], err: [], lat: [] };
	regions.forEach((region, i) => {
		const [requests, errors, latency] = results.slice(i * metrics.length, (i + 1) * metrics.length).map((r) => r.data);
		const points = trafficPoints([requests, errors, undefined, latency]);
		for (const row of rows.filter((p) => p.region === region)) {
			const own = points.filter((p) => p.resourceId === row.resource.id);
			for (const card of TRAFFIC_CARDS) {
				const values = own.flatMap((p) => {
					const v = card.value(p);
					return v === undefined ? [] : [{ t: p.time, v }];
				});
				if (values.length === 0) continue;
				series[card.key].push({
					key: row.key,
					name: multi.has(row.resource.id) ? `${row.resource.raw.name} ${region}` : row.resource.raw.name,
					color: row.resource.color,
					dashed: row.dashed,
					limits: [],
					points: values,
				});
			}
		}
	});
	return {
		series,
		isLoading: results.some((r) => r.isLoading),
		error: results.find((r) => r.error !== null)?.error ?? null,
		refetch: async () => await Promise.all(results.map(async (r) => await r.refetch())),
	};
}

function usePlacementSizing(rows: Placement[], recent: Deployment[], environmentId: string, from: number) {
	const transport = useTransport();
	const ids = [...new Set(rows.map((p) => p.deploymentId).filter((id) => id !== ""))];
	const queries = useQueries({ queries: ids.map((id) => currentDeploymentOptions(transport, id)) });
	const current = new Map(
		queries.flatMap((q) => (q.data?.deployment === undefined ? [] : [q.data.deployment])).map((d) => [d.id, d]),
	);
	const byResource = Map.groupBy(recent, (d) => d.resourceId);
	// Deployments that started after the current one never replaced it, so their sizing never ran.
	const known = (p: Placement) => {
		const deployment = current.get(p.deploymentId);
		const history = byResource.get(p.resource.id) ?? [];
		if (deployment === undefined) return history;
		const start = startedIn(deployment, p.region) ?? Infinity;
		return [...history.filter((d) => (startedIn(d, p.region) ?? -Infinity) <= start), deployment];
	};
	// A placement whose known deployments all started inside the window needs the one running before it.
	const startsInWindow = (p: Placement) => {
		const starts = known(p).flatMap((d) => startedIn(d, p.region) ?? []);
		return starts.length > 0 && starts.every((t) => t > from);
	};
	const earlierFor = [...new Set(rows.filter(startsInWindow).map((p) => p.resource.id))];
	const until = timestampFromMs(Math.floor(from / HOUR_MS) * HOUR_MS);
	const earlierQueries = useQueries({
		queries: earlierFor.map((resourceId) =>
			createQueryOptions(
				listDeployments,
				{ resourceId, environmentId, until, pageSize: EARLIER_DEPLOYS },
				{ transport },
			),
		),
	});
	const earlier = new Map(earlierFor.map((id, i) => [id, earlierQueries[i]?.data?.deployments ?? []]));
	const sizing = new Map<string, PlacementSizing>();
	for (const p of rows) {
		const deployment = current.get(p.deploymentId);
		const deployed = deployment === undefined ? undefined : regionTarget(getServiceSpec(deployment), p.region);
		const spec = p.resource.raw.spec?.spec;
		const intent = spec?.case === "service" ? spec.value.regions[p.region] : undefined;
		sizing.set(p.key, {
			deployments: [...(earlier.get(p.resource.id) ?? []), ...known(p)],
			target: deployed ?? intent,
		});
	}
	return { sizing, isLoading: queries.some((q) => q.isLoading) || earlierQueries.some((q) => q.isLoading) };
}

/** The environment's deployments created from a day before the window through its end. */
function useWindowDeployments(workspaceId: string, environmentId: string, from: number, to: number, live: boolean) {
	const transport = useTransport();
	const query = useQuery({
		...workspaceDeploymentsOptions(transport, workspaceId, environmentId, {
			sinceMs: Math.floor((from - DEPLOY_LEAD_MS) / HOUR_MS) * HOUR_MS,
			untilMs: live ? undefined : Math.floor(to / MINUTE_MS) * MINUTE_MS + MINUTE_MS,
		}),
		placeholderData: keepPreviousData,
		meta: { persist: false },
	});
	return query.data?.deployments ?? [];
}

function deployMarkersOf(
	visible: ObsResource[],
	deployments: Deployment[],
	environmentId: string,
	from: number,
	to: number,
): DeployMarker[] {
	const byResource = Map.groupBy(deployments, (d) => d.resourceId);
	const markers: DeployMarker[] = [];
	visible.forEach((r) => {
		for (const d of byResource.get(r.id) ?? []) {
			const t = readyMs(d);
			if (t === undefined || t < from || t > to || d.environmentId !== environmentId) continue;
			const tag = formatShortId(d.id);
			const label = `${r.raw.name} ${tag}`;
			for (const p of d.placements) {
				markers.push({
					t,
					label,
					title: `Deployed ${label} in ${p.region}`,
					color: r.color,
					tag,
					resourceId: r.id,
					resourceName: r.raw.name,
					region: p.region,
				});
			}
		}
	});
	return markers;
}

function Meter({ value, format }: { value: { v: number; limit: number } | undefined; format: (v: number) => string }) {
	if (value === undefined) return <span className="text-fg4">—</span>;
	const percent = (value.v / value.limit) * PERCENT;
	return (
		<span className="grid w-full grid-cols-[minmax(0,1fr)_auto] items-center gap-2">
			<span className="h-1 overflow-hidden rounded-[2px] bg-line2">
				<span
					className={cn("block h-full rounded-[2px]", percent >= WARN_PERCENT ? "bg-warn" : "bg-fg2")}
					style={{ width: `${String(Math.min(PERCENT, percent))}%` }}
				/>
			</span>
			<span className="text-right font-mono text-[12px] whitespace-nowrap text-fg2">
				{format(value.v)} <span className="text-fg3">{formatPercent(percent)}</span>
			</span>
		</span>
	);
}

export function MetricsView() {
	const timePref = useTimePreferences();
	const { orgId, workspaceId, resources, resourcesLoading, tokens, range, maxMs, goTo, setQuery, text } = useObs();
	const navigate = useNavigate();
	const [sort, setSort] = useQueryState("sort", metricSortParser);
	const { active: environment, isLoading: environmentLoading } = useEnvironments();
	const now = useNow(NOW_TICK_MS);
	const { from, to } = resolveWindow(range, now.getTime());
	const spanMs = windowSpan(range);
	const endMs = range.kind === "absolute" ? range.to : undefined;
	const visible = filteredResources("metrics", resources, tokens);
	const rows = placements(visible, environment?.id ?? "", liveTokens("metrics", tokens, "region"));
	const recent = useWindowDeployments(workspaceId, environment?.id ?? "", from, to, range.kind === "relative");
	const { sizing, isLoading: sizingLoading } = usePlacementSizing(rows, recent, environment?.id ?? "", from);
	const cpu = useComputeSeries(CPU, rows, sizing, spanMs, endMs);
	const mem = useComputeSeries(MEMORY, rows, sizing, spanMs, endMs);
	const traffic = useTrafficSeries(rows, spanMs, endMs);
	const deployMarkers = deployMarkersOf(visible, recent, environment?.id ?? "", from, to);
	const [hoverKey, setHoverKey] = useState<string | null>(null);
	const [focusTs] = useQueryState("at", msParser);
	const markers: ChartMarker[] = [
		...deployMarkers,
		...(focusTs !== null && focusTs >= from && focusTs <= to
			? [
					{
						t: focusTs,
						label: `focus ${formatHourMinute(focusTs, timePref)}`,
						title: "Selected time",
						color: "var(--fg)",
					},
				]
			: []),
	];
	const actions = (t: number): PinAction[] => {
		const around = aroundWindow(t, Date.now());
		const near = deployMarkers.filter((m) => Math.abs(m.t - t) <= (to - from) * DEPLOY_PICK_SHARE);
		return [
			{
				key: "logs",
				icon: <ScrollTextIcon />,
				label: `Logs around ${formatHourMinute(t, timePref)}`,
				sub: "±5 minutes, same filters",
				run: () => {
					goTo("logs", { range: around, focusTs: t });
				},
			},
			{
				key: "traces",
				icon: <WaypointsIcon />,
				label: `Traces around ${formatHourMinute(t, timePref)}`,
				sub: "±5 minutes, same filters",
				run: () => {
					goTo("traces", { range: around });
				},
			},
			{
				key: "events",
				icon: <BellIcon />,
				label: `Events around ${formatHourMinute(t, timePref)}`,
				sub: "deploys, restarts, scaling",
				run: () => {
					goTo("events", { range: around });
				},
			},
			...near.map((m, i) => ({
				separated: i === 0,
				key: `deploy-${m.resourceId}-${String(m.t)}`,
				icon: <RocketIcon />,
				label: `Deploy ${m.tag}`,
				sub: `${m.resourceName} · ${m.region} · ${formatHourMinute(m.t, timePref)}`,
				run: () => {
					void navigate(resourcePath(orgId, workspaceId, m.resourceId));
				},
			})),
		];
	};
	const error = cpu.error ?? mem.error ?? traffic.error;
	const loading = cpu.isLoading || mem.isLoading || sizingLoading || environmentLoading || resourcesLoading;
	const sorting = [sort];
	const table = useDataTable("metrics-placements", {
		columns: placementColumns(cpu.latest, mem.latest, traffic.series),
		data: rows.toSorted((a, b) => a.key.localeCompare(b.key)),
		getRowId: (p) => p.key,
		state: { sorting },
		onSortingChange: (updater) => {
			const next = functionalUpdate(updater, sorting)[0];
			const id = SORT_KEYS.find((key) => key === next?.id);
			if (next !== undefined && id !== undefined) void setSort({ id, desc: next.desc });
		},
	});

	if (!loading && error !== null && isUnimplemented(error)) {
		return (
			<>
				<ViewHeader />
				<ObsHero
					view="metrics"
					title="Metrics aren't available here yet"
					body="This installation has no metrics store connected. CPU and memory appear once it does."
				/>
			</>
		);
	}
	if (!loading && error !== null) {
		return (
			<>
				<ViewHeader />
				<QueryBar valueCounts={(key) => valueCounts(key, resources)} />
				<ErrorState
					error={error}
					what="metrics"
					rpc={queryMetrics}
					onRetry={async () => await Promise.all([cpu.refetch(), mem.refetch(), traffic.refetch()])}
				/>
			</>
		);
	}
	if (!loading && cpu.series.length === 0 && mem.series.length === 0 && tokens.length === 0) {
		return (
			<>
				<ViewHeader />
				<ObsHero
					view="metrics"
					title={coversWholeRange(range, maxMs) ? "No metrics collected" : "No metrics in this range"}
					body="CPU and memory start as soon as a resource is up. Requests and latency come from resources with a public address."
					action={
						<>
							<Button
								nativeButton={false}
								render={<Link to={newResourcePath(orgId, workspaceId, environment ? { env: environment.slug } : {})} />}
							>
								<PlusIcon />
								New resource
							</Button>
							<WidenRangeButton />
						</>
					}
				/>
			</>
		);
	}

	const focusRow = (p: Placement) => {
		setQuery(
			[
				...tokens.filter((t) => t.key !== "resource" && t.key !== "region"),
				{ neg: false, key: "resource", value: p.resource.slug },
				{ neg: false, key: "region", value: p.region },
			],
			text,
		);
	};

	const cards = [
		{ def: CPU, result: cpu },
		{ def: MEMORY, result: mem },
	];
	const trafficCards = TRAFFIC_CARDS.filter((card) => traffic.series[card.key].length > 0);

	return (
		<>
			<ViewHeader />
			<QueryBar valueCounts={(key) => valueCounts(key, resources)} />
			<Section title="Compute" card={false}>
				<div className="grid grid-cols-1 gap-3 lg:grid-cols-2">
					{cards.map(({ def, result }) => (
						<section
							key={def.key}
							aria-label={def.title}
							className="flex min-w-0 flex-col gap-3.5 rounded-card bg-panel px-5 pt-[18px] pb-4"
						>
							<div className="flex items-baseline gap-2">
								<h3 className="m-0 text-[15px] font-medium tracking-[-0.01em]">{def.title}</h3>
								<span className="font-mono text-[12px] text-fg3">% of limit, peak per bucket</span>
							</div>
							{result.isLoading || environmentLoading ? (
								<Skeleton className="w-full" style={{ height: CHART_HEIGHT }} />
							) : result.series.length === 0 ? (
								<div
									className="flex items-center justify-center rounded-lg border border-dashed border-line2 text-sm text-fg3"
									style={{ height: CHART_HEIGHT }}
								>
									No data in this range
								</div>
							) : (
								<MetricChart
									title={def.title}
									series={result.series}
									from={from}
									to={to}
									fmt={def.format}
									highlight={hoverKey}
									markers={markers}
									height={CHART_HEIGHT}
									actions={actions}
								/>
							)}
						</section>
					))}
				</div>
			</Section>

			{trafficCards.length > 0 && (
				<Section title="Traffic" card={false}>
					<div className="grid grid-cols-1 gap-3 lg:grid-cols-3">
						{trafficCards.map((card) => (
							<section
								key={card.key}
								aria-label={card.title}
								className="flex min-w-0 flex-col gap-3.5 rounded-card bg-panel px-5 pt-[18px] pb-4"
							>
								<div className="flex items-baseline gap-2">
									<h3 className="m-0 text-[15px] font-medium tracking-[-0.01em]">{card.title}</h3>
									<span className="font-mono text-[12px] text-fg3">{card.unit}</span>
								</div>
								<MetricChart
									title={card.title}
									series={traffic.series[card.key]}
									from={from}
									to={to}
									fmt={card.format}
									unit={card.axisUnit}
									highlight={hoverKey}
									markers={markers}
									height={TRAFFIC_HEIGHT}
									actions={actions}
								/>
								<ul className="m-0 flex list-none flex-wrap gap-x-3.5 gap-y-1 p-0 text-[12px] text-fg2">
									{traffic.series[card.key].map((s) => (
										<li key={s.key} className="flex items-center gap-1.5">
											<span
												className={cn("w-3 border-t-2", s.dashed && "border-dashed")}
												style={{ borderColor: s.color }}
											/>
											{s.name}
											<span className="font-mono text-fg3">{card.format(s.points.at(-1)?.v ?? 0)}</span>
										</li>
									))}
								</ul>
							</section>
						))}
					</div>
				</Section>
			)}

			<Section title="By resource and region" card={false}>
				<div className="min-w-0 rounded-card bg-panel">
					<DataTable
						table={table}
						label="By resource and region"
						className="text-[12.5px] [--row-h:38px] [&_td]:py-1"
						empty={<p className="px-3 py-6 text-center text-fg3">Nothing matches these filters.</p>}
						onRowClick={(row) => {
							focusRow(row.original);
						}}
						onRowHover={(row) => {
							setHoverKey(row?.original.key ?? null);
						}}
					/>
				</div>
				<span className="flex items-center gap-1.5 text-[12px] text-fg3">
					Ready replicas over time
					<SoonTag />
				</span>
			</Section>
		</>
	);
}

type Latest = Map<string, { v: number; limit: number }>;

function placementColumns(
	cpu: Latest,
	mem: Latest,
	traffic: Record<TrafficKey, ChartSeries[]>,
): DataColumn<Placement>[] {
	return [
		{
			id: "resource",
			accessorFn: (p) => p.resource.raw.name,
			sortFn: (a, b) => a.original.resource.raw.name.localeCompare(b.original.resource.raw.name),
			header: "Resource",
			size: 140,
			meta: { grow: true },
			cell: ({ row: { original: p } }) => (
				<span className="flex min-w-0 items-center gap-1.5">
					<span className="size-2 shrink-0 rounded-[2px]" style={{ background: p.resource.color }} />
					<CellText text={p.resource.raw.name} />
				</span>
			),
		},
		{
			id: "region",
			accessorFn: (p) => p.region,
			sortFn: (a, b) => a.original.region.localeCompare(b.original.region),
			header: "Region",
			meta: { fit: true },
			cell: ({ row }) => <CellText text={row.original.region} className="font-mono text-fg2" />,
		},
		{
			id: "replicas",
			accessorFn: (p) => p.ready ?? -1,
			header: "Replicas",
			meta: { fit: true },
			sortDescFirst: true,
			cell: ({ row: { original: p } }) => {
				const short = p.ready !== null && p.desired !== null && p.ready < p.desired;
				return (
					<span className={cn("truncate font-mono", short ? "text-warn-fg" : "text-fg2")}>
						{p.ready !== null && p.desired !== null ? `${String(p.ready)} of ${String(p.desired)}` : "—"}
					</span>
				);
			},
		},
		{
			id: "cpu",
			accessorFn: (p) => cpu.get(p.key)?.v ?? -1,
			header: "CPU (latest)",
			size: 180,
			sortDescFirst: true,
			cell: ({ row }) => <Meter value={cpu.get(row.original.key)} format={CPU.format} />,
		},
		{
			id: "mem",
			accessorFn: (p) => mem.get(p.key)?.v ?? -1,
			header: "Memory (latest)",
			size: 180,
			sortDescFirst: true,
			cell: ({ row }) => <Meter value={mem.get(row.original.key)} format={MEMORY.format} />,
		},
		...TRAFFIC_CARDS.map((card): DataColumn<Placement> => ({
			id: card.key,
			header: card.column,
			meta: { fit: true },
			cell: ({ row }) => {
				const value = traffic[card.key].find((s) => s.key === row.original.key)?.points.at(-1)?.v;
				return (
					<span className={cn("font-mono", value === undefined ? "text-fg4" : "text-fg2")}>
						{value === undefined ? "—" : card.format(value)}
					</span>
				);
			},
		})),
	];
}

function valueCounts(key: string, resources: ObsResource[]): [string, number][] {
	if (key === "resource") return resources.map((r) => [r.slug, 0]);
	if (key === "region") return [...new Set(resources.flatMap((r) => r.regions))].map((g) => [g, 0]);
	return [];
}
