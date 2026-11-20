import { useTimePreferences } from "@/lib/use-time-preferences";
import { createQueryOptions, useTransport } from "@connectrpc/connect-query";
import { useQueries } from "@tanstack/react-query";
import { functionalUpdate } from "@tanstack/react-table";
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
import { metricsQueryOptions } from "@/hooks/useQueryMetrics";
import { formatPercent } from "@/lib/compute-chart";
import { getServiceSpec, regionTarget } from "@/lib/deployment-utils";
import { formatCpu, formatMemory, parseCpuMilli, parseMemMi } from "@/lib/quantity";
import { isUnimplemented } from "@/lib/error-handler";
import { sortParser } from "@/lib/sort-parser";
import { aroundWindow, msParser, resolveWindow, windowSpan } from "@/lib/obs-time";
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
	limit: (sizing: { cpu: string; memory: string }) => number;
}

const CPU: MetricDef = {
	key: "cpu",
	metric: "k8s.pod.cpu_limit_utilization",
	title: "CPU",
	format: formatCpu,
	limit: (sizing) => parseCpuMilli(sizing.cpu),
};
const MEMORY: MetricDef = {
	key: "mem",
	metric: "k8s.pod.memory.working_set_limit_utilization",
	title: "Memory",
	format: formatMemory,
	limit: (sizing) => parseMemMi(sizing.memory),
};

const TRAFFIC = ["Requests", "5xx responses", "Latency, p95"];
const SOON_COLUMNS = ["Requests", "5xx", "p95"];
const PERCENT = 100;
const NOW_TICK_MS = 60_000;
const CHART_HEIGHT = 190;
const DEPLOYS_PER_RESOURCE = 20;
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
	limits: Map<string, { cpu: string; memory: string }>,
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
			const sizing = row === undefined ? undefined : limits.get(row.key);
			const limit = sizing === undefined ? 0 : def.limit(sizing);
			if (row === undefined || limit <= 0) continue;
			const points = [...pts].map(([t, v]) => ({ t, v })).toSorted((a, b) => a.t - b.t);
			series.push({
				key: row.key,
				name: multi.has(id) ? `${row.resource.raw.name} ${region}` : row.resource.raw.name,
				color: row.resource.color,
				dashed: row.dashed,
				limit,
				points,
			});
			const last = points.at(-1);
			if (last !== undefined) latest.set(row.key, { v: (last.v / PERCENT) * limit, limit });
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

function usePlacementSizing(rows: Placement[], visible: ObsResource[], environmentId: string) {
	const transport = useTransport();
	const queries = useQueries({
		queries: visible.map((r) =>
			createQueryOptions(listDeployments, { resourceId: r.id, pageSize: DEPLOYS_PER_RESOURCE }, { transport }),
		),
	});
	const sizing = new Map<string, { cpu: string; memory: string }>();
	const isLoading = queries.some((q) => q.isLoading);
	visible.forEach((r, i) => {
		const deployments = queries[i]?.data?.deployments ?? [];
		for (const p of rows.filter((row) => row.resource.id === r.id)) {
			const deployment =
				deployments.find((d) => d.id === p.deploymentId) ??
				deployments.find((d) => d.environmentId === environmentId && d.placements.some((x) => x.region === p.region));
			const deployed = deployment === undefined ? undefined : regionTarget(getServiceSpec(deployment), p.region);
			const intent = r.raw.spec?.spec.case === "service" ? r.raw.spec.spec.value.regions[p.region] : undefined;
			const target = deployed ?? intent;
			if (target !== undefined)
				sizing.set(p.key, {
					cpu: target.cpu ?? "",
					memory: target.memory ?? "",
				});
		}
	});
	return { sizing, isLoading };
}

function useDeployMarkers(visible: ObsResource[], environmentId: string, from: number, to: number): DeployMarker[] {
	const transport = useTransport();
	const queries = useQueries({
		queries: visible.map((r) =>
			createQueryOptions(listDeployments, { resourceId: r.id, pageSize: DEPLOYS_PER_RESOURCE }, { transport }),
		),
	});
	const markers: DeployMarker[] = [];
	visible.forEach((r, i) => {
		for (const d of queries[i]?.data?.deployments ?? []) {
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
	const { sizing, isLoading: sizingLoading } = usePlacementSizing(rows, visible, environment?.id ?? "");
	const cpu = useComputeSeries(CPU, rows, sizing, spanMs, endMs);
	const mem = useComputeSeries(MEMORY, rows, sizing, spanMs, endMs);
	const deployMarkers = useDeployMarkers(visible, environment?.id ?? "", from, to);
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
			...near.map((m) => ({
				key: `deploy-${m.resourceId}-${String(m.t)}`,
				icon: <RocketIcon />,
				label: `Deploy ${m.tag}`,
				sub: `${m.resourceName} · ${m.region} · ${formatHourMinute(m.t, timePref)}`,
				run: () => {
					void navigate(resourcePath(orgId, workspaceId, m.resourceId));
				},
			})),
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
				sub: "deploys, restarts, health checks",
				run: () => {
					goTo("events", { range: around });
				},
			},
		];
	};
	const error = cpu.error ?? mem.error;
	const loading = cpu.isLoading || mem.isLoading || sizingLoading || environmentLoading || resourcesLoading;
	const sorting = [sort];
	const table = useDataTable("metrics-placements", {
		columns: placementColumns(cpu.latest, mem.latest),
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
					onRetry={async () => await Promise.all([cpu.refetch(), mem.refetch()])}
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

			<Section title="Traffic" card={false}>
				<div className="grid grid-cols-1 gap-3 md:grid-cols-3">
					{TRAFFIC.map((title) => (
						<div key={title} className="flex flex-col gap-2 rounded-card bg-panel px-5 py-4">
							<span className="flex items-baseline gap-2">
								<span className="text-[15px] font-medium tracking-[-0.01em] text-fg3">{title}</span>
								<SoonTag />
							</span>
							<div className="h-12 rounded-lg border border-dashed border-line2" />
						</div>
					))}
				</div>
			</Section>

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

function placementColumns(cpu: Latest, mem: Latest): DataColumn<Placement>[] {
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
		...SOON_COLUMNS.map((label): DataColumn<Placement> => ({
			id: label,
			header: () => (
				<span className="flex items-center gap-1.5 whitespace-nowrap">
					{label} <SoonTag />
				</span>
			),
			meta: { label, fit: true },
			cell: () => <span className="text-fg4">—</span>,
		})),
	];
}

function valueCounts(key: string, resources: ObsResource[]): [string, number][] {
	if (key === "resource") return resources.map((r) => [r.slug, 0]);
	if (key === "region") return [...new Set(resources.flatMap((r) => r.regions))].map((g) => [g, 0]);
	return [];
}
