import { useState } from "react";
import { createQueryOptions, useTransport } from "@connectrpc/connect-query";
import { useQueries } from "@tanstack/react-query";
import { listDeployments } from "@gen/loco/deployment/v1/deployment-DeploymentService_connectquery";

import { Skeleton } from "@/components/design/Skeleton";
import { SoonTag } from "@/components/design/SoonTag";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { useNow } from "@/hooks/useNow";
import { useQueryMetrics } from "@/hooks/useQueryMetrics";
import { getErrorMessage } from "@/lib/error-handler";
import { timeRangeMs } from "@/lib/obs";
import { cn, formatShortId } from "@/lib/utils";

import { fitRange, selectedResources, useObs, type ObsResource } from "./context";
import { fmtClock, tsMs } from "./format";
import { MetricCard, MetricChart, type ChartMarker, type ChartSeries } from "./MetricChart";
import { ObsGate } from "./ObsGate";
import { ResourceMenu, TimeRangeMenu } from "./Toolbar";

interface MetricDef {
	key: string;
	metric: string;
	title: string;
}

const COMPUTE: MetricDef[] = [
	{ key: "cpu", metric: "k8s.pod.cpu_limit_utilization", title: "CPU" },
	{ key: "mem", metric: "k8s.pod.memory_limit_utilization", title: "Memory" },
];

const fmtPct = (v: number) => `${v < 10 ? v.toFixed(1) : Math.round(v).toString()}%`;

export function MetricsView() {
	const { resources, selected } = useObs();
	const [hidden, setHidden] = useState<string[]>([]);
	const sel = selectedResources(resources, selected);
	const visible = sel.filter((r) => !hidden.includes(r.name));

	return (
		<>
			<div className="flex flex-wrap items-center gap-2">
				<ResourceMenu />
				<div className="flex-1" />
				<TimeRangeMenu maxRange="24h" />
			</div>
			<ObsGate>
				<div className="flex flex-col gap-5">
					<div className="flex flex-wrap items-center gap-3">
						<div className="flex min-w-0 flex-1 flex-wrap items-center gap-1.5">
							{sel.map((r) => {
								const off = hidden.includes(r.name);
								return (
									<button
										key={r.id}
										type="button"
										title={off ? "Show" : "Hide"}
										onClick={() => {
											setHidden(off ? hidden.filter((x) => x !== r.name) : [...hidden, r.name]);
										}}
										className={cn(
											"flex h-[30px] cursor-pointer items-center gap-2 rounded-lg border px-2.5 text-foreground hover:border-fg4",
											off ? "border-line bg-transparent opacity-50" : "border-line2 bg-background",
										)}
									>
										<span className="h-[3px] w-2.5 rounded-[2px]" style={{ background: r.color }} />
										<span className="font-medium">{r.name}</span>
									</button>
								);
							})}
						</div>
						<div className="flex items-center gap-2">
							<span className="text-sm text-fg3">Split by</span>
							<ToggleGroup variant="segmented" value={["service"]}>
								<ToggleGroupItem value="service" className="h-[26px]! text-sm">
									Service
								</ToggleGroupItem>
								<ToggleGroupItem value="replica" disabled className="h-[26px]! gap-1.5 text-sm">
									Replica
									<SoonTag />
								</ToggleGroupItem>
							</ToggleGroup>
						</div>
					</div>
					<TrafficSection />
					<ComputeSection visible={visible} />
				</div>
			</ObsGate>
		</>
	);
}

function SectionTitle({ children }: { children: string }) {
	return <span className="text-sm font-semibold tracking-[0.04em] text-fg3 uppercase">{children}</span>;
}

function SoonChart({ height }: { height: number }) {
	return (
		<div
			className="flex items-center justify-center rounded-sm border border-dashed border-line2 text-sm text-fg4"
			style={{ height }}
		>
			Request metrics aren't collected yet
		</div>
	);
}

function TrafficSection() {
	const soonTitle = (t: string) => (
		<span className="flex items-center gap-1.5">
			{t}
			<SoonTag />
		</span>
	);
	return (
		<div className="flex flex-col gap-2.5">
			<SectionTitle>Traffic</SectionTitle>
			<div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
				<MetricCard title={soonTitle("Requests")} unit="req/s" big="—" delta="" deltaBad={false} bigNote="" muted>
					<SoonChart height={200} />
				</MetricCard>
				<MetricCard title={soonTitle("5xx errors")} unit="req/s" big="—" delta="" deltaBad={false} bigNote="" muted>
					<SoonChart height={200} />
				</MetricCard>
				<MetricCard
					title={soonTitle("Latency")}
					unit="p95"
					big="—"
					delta=""
					deltaBad={false}
					bigNote=""
					span
					muted
					actions={
						<ToggleGroup variant="segmented" value={["p95"]} className="rounded-md p-0.5">
							{["p50", "p95", "p99"].map((a) => (
								<ToggleGroupItem key={a} value={a} disabled className="h-[22px]! rounded-sm! px-2 text-sm">
									{a}
								</ToggleGroupItem>
							))}
						</ToggleGroup>
					}
				>
					<SoonChart height={220} />
				</MetricCard>
			</div>
		</div>
	);
}

function useDeployMarkers(visible: ObsResource[], from: number, to: number): ChartMarker[] {
	const transport = useTransport();
	const queries = useQueries({
		queries: visible.map((r) => createQueryOptions(listDeployments, { resourceId: r.id, pageSize: 20 }, { transport })),
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

function ComputeSection({ visible }: { visible: ObsResource[] }) {
	const { range, metricsFocusTs } = useObs();
	const now = useNow(60_000);
	const to = now.getTime();
	const from = to - timeRangeMs(range);
	const deployMarkers = useDeployMarkers(visible, from, to);
	const focus: ChartMarker[] =
		metricsFocusTs !== null && metricsFocusTs >= from
			? [{ t: metricsFocusTs, label: `log · ${fmtClock(metricsFocusTs)}`, title: "Selected log", color: "var(--fg)" }]
			: [];
	return (
		<div className="flex flex-col gap-2.5">
			<SectionTitle>Compute</SectionTitle>
			<div className="grid grid-cols-1 gap-4 lg:grid-cols-2">
				{COMPUTE.map((d) => (
					<ComputeChart key={d.key} def={d} visible={visible} from={from} to={to} markers={[...deployMarkers, ...focus]} />
				))}
			</div>
		</div>
	);
}

function ComputeChart({
	def,
	visible,
	from,
	to,
	markers,
}: {
	def: MetricDef;
	visible: ObsResource[];
	from: number;
	to: number;
	markers: ChartMarker[];
}) {
	const { workspaceId, transports, range, goTo } = useObs();
	const result = useQueryMetrics({
		clusterTransports: transports,
		workspaceId,
		resourceIds: visible.map((r) => r.id),
		timeRange: range,
		metricName: def.metric,
		aggregation: "max",
		enabled: visible.length > 0,
	});

	const byId = new Map(visible.map((r) => [r.id, r]));
	const merged = new Map<string, Map<number, number>>();
	for (const s of result.series) {
		const pts = merged.get(s.resourceId) ?? new Map<number, number>();
		for (const p of s.points) {
			const t = tsMs(p.timestamp);
			pts.set(t, Math.max(pts.get(t) ?? 0, p.value * 100));
		}
		merged.set(s.resourceId, pts);
	}
	const series: ChartSeries[] = [];
	for (const [id, pts] of merged) {
		const r = byId.get(id);
		if (r === undefined) continue;
		const points = [...pts.entries()].map(([t, v]) => ({ t, v })).sort((a, b) => a.t - b.t);
		series.push({ name: r.name, color: r.color, points });
	}

	const peak = Math.max(0, ...series.flatMap((s) => s.points.map((p) => p.v)));
	const max = Math.max(112, peak * 1.15);
	const lastOf = (s: ChartSeries) => s.points.at(-1)?.v ?? 0;
	const firstOf = (s: ChartSeries) => s.points[0]?.v ?? 0;
	const cur = series.length > 0 ? Math.max(...series.map(lastOf)) : 0;
	const start = series.length > 0 ? Math.max(...series.map(firstOf)) : 0;
	const pct = start > 0 ? ((cur - start) / start) * 100 : 0;
	const hasData = series.some((s) => s.points.length > 0);

	const onPick = (t: number) => {
		const single = visible.length === 1 ? visible[0] : undefined;
		goTo("logs", {
			range: fitRange(t, to),
			tokens: single !== undefined ? [{ neg: false, key: "resource", value: single.name }] : [],
			focusTs: t,
		});
	};

	return (
		<MetricCard
			title={def.title}
			unit="% of limit · busiest replica"
			big={hasData ? fmtPct(cur) : "—"}
			delta={hasData && Math.abs(pct) >= 1 ? `${pct > 0 ? "↑" : "↓"} ${Math.abs(pct).toFixed(0)}%` : ""}
			deltaBad={pct > 0}
			bigNote={hasData ? (series.length > 1 ? "now · highest" : "now") : ""}
		>
			{result.isLoading ? (
				<Skeleton className="h-[200px] w-full" />
			) : result.error !== null ? (
				<div className="flex h-[200px] items-center justify-center text-sm text-bad-fg">
					{getErrorMessage(result.error, "Failed to load metrics")}
				</div>
			) : !hasData ? (
				<div className="flex h-[200px] items-center justify-center rounded-sm border border-dashed border-line2 text-sm text-fg3">
					No data in this range
				</div>
			) : (
				<MetricChart
					series={series}
					from={from}
					to={to}
					max={max}
					fmt={fmtPct}
					limit={{ v: 100, label: "limit" }}
					markers={markers}
					height={200}
					onPick={onPick}
				/>
			)}
		</MetricCard>
	);
}
