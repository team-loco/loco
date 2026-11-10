import { createQueryOptions, useTransport } from "@connectrpc/connect-query";
import { useQueries } from "@tanstack/react-query";
import { listDeployments } from "@gen/loco/deployment/v1/deployment-DeploymentService_connectquery";
import { BellIcon, ScrollTextIcon, WaypointsIcon } from "lucide-react";

import { Section } from "@/components/design/Page";
import { Skeleton } from "@/components/design/Skeleton";
import { SoonTag } from "@/components/design/SoonTag";
import { useEnvironments } from "@/hooks/useEnvironment";
import { useNow } from "@/hooks/useNow";
import { metricsQueryOptions } from "@/hooks/useQueryMetrics";
import { formatPercent, percentAxis } from "@/lib/compute-chart";
import { getErrorMessage, isUnimplemented } from "@/lib/error-handler";
import { aroundWindow, resolveWindow, windowSpan } from "@/lib/obs-time";
import { logAnchor } from "@/lib/log-history";
import { formatHourMinute, tsMs } from "@/lib/time";
import { cn, formatShortId } from "@/lib/utils";

import { filteredResources, useObs, type ObsResource } from "./context";
import { MetricChart, type ChartMarker, type ChartSeries, type PinAction } from "./MetricChart";
import { ObsHero } from "./ObsEmpty";
import { liveTokens, narrow, type Token } from "./query";
import { QueryBar } from "./QueryBar";
import { ViewHeader } from "./ViewHeader";

interface MetricDef {
	key: "cpu" | "mem";
	metric: string;
	title: string;
}

const CPU: MetricDef = { key: "cpu", metric: "k8s.pod.cpu_limit_utilization", title: "CPU" };
const MEMORY: MetricDef = { key: "mem", metric: "k8s.pod.memory_limit_utilization", title: "Memory" };

const TRAFFIC = ["Requests", "5xx responses", "Latency, p95"];
const PERCENT = 100;
const NOW_TICK_MS = 60_000;
const CHART_HEIGHT = 190;
const DEPLOYS_PER_RESOURCE = 20;
const WARN_PERCENT = 85;

interface Placement {
	key: string;
	resource: ObsResource;
	region: string;
	dashed: boolean;
	ready: number | null;
	desired: number | null;
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
			});
		});
	}
	return out;
}

function useComputeSeries(def: MetricDef, rows: Placement[], spanMs: number, endMs: number | undefined) {
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
	const latest = new Map<string, number>();
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
			const points = [...pts].map(([t, v]) => ({ t, v })).toSorted((a, b) => a.t - b.t);
			series.push({
				key: `s${String(series.length)}`,
				name: multi.has(id) ? `${row.resource.name} ${region}` : row.resource.name,
				color: row.resource.color,
				dashed: row.dashed,
				points,
			});
			const last = points.at(-1);
			if (last !== undefined) latest.set(row.key, last.v);
		}
	});
	return {
		series,
		latest,
		isLoading: results.some((r) => r.isLoading),
		error: results.find((r) => r.error !== null)?.error ?? null,
	};
}

function useDeployMarkers(visible: ObsResource[], from: number, to: number): ChartMarker[] {
	const transport = useTransport();
	const queries = useQueries({
		queries: visible.map((r) =>
			createQueryOptions(listDeployments, { resourceId: r.id, pageSize: DEPLOYS_PER_RESOURCE }, { transport }),
		),
	});
	const markers: ChartMarker[] = [];
	visible.forEach((r, i) => {
		for (const d of queries[i]?.data?.deployments ?? []) {
			const t = tsMs(d.startedAt ?? d.createdAt);
			if (t < from || t > to) continue;
			const label = `${r.name} · ${formatShortId(d.id)}`;
			markers.push({ t, label, title: `Deployed ${label}`, color: r.color });
		}
	});
	return markers;
}

function Meter({ value }: { value: number | undefined }) {
	if (value === undefined) return <span className="text-fg4">—</span>;
	return (
		<span className="grid grid-cols-[minmax(0,1fr)_44px] items-center gap-2">
			<span className="h-1 overflow-hidden rounded-[2px] bg-line2">
				<span
					className={cn("block h-full rounded-[2px]", value >= WARN_PERCENT ? "bg-warn" : "bg-fg2")}
					style={{ width: `${String(Math.min(PERCENT, value))}%` }}
				/>
			</span>
			<span className="text-right font-mono text-[12px] text-fg2">{formatPercent(value)}</span>
		</span>
	);
}

export function MetricsView() {
	const { resources, resourcesLoading, tokens, range, params, goTo, setQuery, text } = useObs();
	const { active: environment, isLoading: environmentLoading } = useEnvironments();
	const now = useNow(NOW_TICK_MS);
	const { from, to } = resolveWindow(range, now.getTime());
	const spanMs = windowSpan(range);
	const endMs = range.kind === "absolute" ? range.to : undefined;
	const visible = filteredResources("metrics", resources, tokens);
	const rows = placements(visible, environment?.id ?? "", liveTokens("metrics", tokens, "region"));
	const cpu = useComputeSeries(CPU, rows, spanMs, endMs);
	const mem = useComputeSeries(MEMORY, rows, spanMs, endMs);
	const deployMarkers = useDeployMarkers(visible, from, to);
	const focusTs = logAnchor(params.get("at"));
	const markers: ChartMarker[] = [
		...deployMarkers,
		...(focusTs !== null && focusTs >= from && focusTs <= to
			? [{ t: focusTs, label: `focus ${formatHourMinute(focusTs)}`, title: "Selected time", color: "var(--fg)" }]
			: []),
	];
	const actions = (t: number): PinAction[] => {
		const around = aroundWindow(t, Date.now());
		return [
			{
				key: "logs",
				icon: <ScrollTextIcon />,
				label: `Logs around ${formatHourMinute(t)}`,
				sub: "±5 minutes, same filters",
				run: () => {
					goTo("logs", { range: around, focusTs: t });
				},
			},
			{
				key: "traces",
				icon: <WaypointsIcon />,
				label: `Traces around ${formatHourMinute(t)}`,
				sub: "±5 minutes, same filters",
				run: () => {
					goTo("traces", { range: around });
				},
			},
			{
				key: "events",
				icon: <BellIcon />,
				label: `Events around ${formatHourMinute(t)}`,
				sub: "deploys, restarts, health checks",
				run: () => {
					goTo("events", { range: around });
				},
			},
		];
	};
	const error = cpu.error ?? mem.error;
	const loading = cpu.isLoading || mem.isLoading || environmentLoading || resourcesLoading;

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
	if (!loading && error === null && cpu.series.length === 0 && mem.series.length === 0 && tokens.length === 0) {
		return (
			<>
				<ViewHeader />
				<QueryBar valueCounts={(key) => valueCounts(key, resources)} />
				<ObsHero
					view="metrics"
					title="No metrics in this range"
					body="CPU and memory appear once a resource is running. Requests, errors and latency come from resources with a public address."
				/>
			</>
		);
	}

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
								<span className="font-mono text-[12px] text-fg3">%</span>
							</div>
							{result.isLoading || environmentLoading ? (
								<Skeleton className="w-full" style={{ height: CHART_HEIGHT }} />
							) : result.error !== null ? (
								<div
									role="alert"
									className="flex items-center justify-center text-sm text-bad-fg"
									style={{ height: CHART_HEIGHT }}
								>
									{getErrorMessage(result.error, "Failed to load metrics")}
								</div>
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
									max={percentAxis(Math.max(0, ...result.series.flatMap((s) => s.points.map((p) => p.v)))).maximum}
									fmt={formatPercent}
									limit={{ v: PERCENT, label: "limit" }}
									markers={markers}
									height={CHART_HEIGHT}
									actions={actions}
								/>
							)}
							<div className="flex flex-wrap gap-x-4 gap-y-1.5 text-[12px] text-fg2">
								{result.series.map((s) => (
									<span key={s.key} className="flex items-center gap-1.5">
										<span
											className={cn("inline-block h-0 w-3 border-t-2", s.dashed && "border-dashed")}
											style={{ borderColor: s.color }}
										/>
										{s.name}
										<span className="font-mono text-[11.5px] text-fg3">{formatPercent(s.points.at(-1)?.v ?? 0)}</span>
									</span>
								))}
							</div>
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
				<div className="overflow-x-auto rounded-card bg-panel px-5 pt-1 pb-3">
					<table className="w-full min-w-[760px] border-collapse text-[12.5px]">
						<thead>
							<tr className="h-[38px] border-b border-line2 text-left text-[12px] text-fg3">
								<th className="px-3 font-normal">Resource</th>
								<th className="px-3 font-normal">Region</th>
								<th className="px-3 font-normal">Replicas</th>
								<th className="w-[170px] px-3 font-normal">CPU</th>
								<th className="w-[170px] px-3 font-normal">Memory</th>
								<th className="px-3 font-normal">
									<span className="flex items-center gap-1.5">
										Requests <SoonTag />
									</span>
								</th>
								<th className="px-3 font-normal">
									<span className="flex items-center gap-1.5">
										5xx <SoonTag />
									</span>
								</th>
								<th className="px-3 font-normal">
									<span className="flex items-center gap-1.5">
										p95 <SoonTag />
									</span>
								</th>
							</tr>
						</thead>
						<tbody>
							{rows.length === 0 && (
								<tr>
									<td colSpan={8} className="px-3 py-6 text-center text-fg3">
										Nothing matches these filters.
									</td>
								</tr>
							)}
							{rows.map((p) => {
								const short = p.ready !== null && p.desired !== null && p.ready < p.desired;
								return (
									<tr key={p.key} className="h-[38px] border-b border-line last:border-b-0 hover:bg-panel2">
										<td className="px-3">
											<button
												type="button"
												onClick={() => {
													setQuery(
														[
															...tokens.filter((t) => t.key !== "resource" && t.key !== "region"),
															{ neg: false, key: "resource", value: p.resource.name },
															{ neg: false, key: "region", value: p.region },
														],
														text,
													);
												}}
												className="flex items-center gap-1.5 rounded-sm text-left hover:underline focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none"
											>
												<span className="size-2 rounded-[2px]" style={{ background: p.resource.color }} />
												{p.resource.name}
											</button>
										</td>
										<td className="px-3 font-mono text-fg2">{p.region}</td>
										<td className={cn("px-3 font-mono", short ? "text-warn-fg" : "text-fg2")}>
											{p.ready !== null && p.desired !== null ? `${String(p.ready)} of ${String(p.desired)}` : "—"}
										</td>
										<td className="px-3">
											<Meter value={cpu.latest.get(p.key)} />
										</td>
										<td className="px-3">
											<Meter value={mem.latest.get(p.key)} />
										</td>
										<td className="px-3 text-fg4">—</td>
										<td className="px-3 text-fg4">—</td>
										<td className="px-3 text-fg4">—</td>
									</tr>
								);
							})}
						</tbody>
					</table>
				</div>
				<span className="flex items-center gap-1.5 text-[12px] text-fg3">
					Ready replicas over time
					<SoonTag />
				</span>
			</Section>
		</>
	);
}

function valueCounts(key: string, resources: ObsResource[]): [string, number][] {
	if (key === "resource") return resources.map((r) => [r.name, 0]);
	if (key === "region") return [...new Set(resources.flatMap((r) => r.regions))].map((g) => [g, 0]);
	return [];
}
