import { useMemo } from "react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { ChartContainer, type ChartConfig } from "@/components/ui/chart";
import { ErrorCard } from "@/components/ErrorCard";
import { Loader2 } from "lucide-react";
import {
	AreaChart,
	Area,
	ResponsiveContainer,
	Tooltip,
	XAxis,
	YAxis,
} from "recharts";
import { useObs } from "./ObsProvider";
import { useQueryMetrics } from "@/hooks/useQueryMetrics";
import type { MetricSeries } from "@/gen/loco/observability/v1/observability_pb";

interface SparklinePoint {
	time: number;
	value: number;
}

function aggregateSeries(series: MetricSeries[]): SparklinePoint[] {
	if (series.length === 0) return [];
	const tsMap = new Map<number, number[]>();
	for (const s of series) {
		for (const pt of s.points) {
			if (!pt.timestamp) continue;
			const ts = Number(pt.timestamp.seconds) * 1000;
			const arr = tsMap.get(ts) ?? [];
			arr.push(pt.value);
			tsMap.set(ts, arr);
		}
	}
	return Array.from(tsMap.entries())
		.sort(([a], [b]) => a - b)
		.map(([time, vals]) => ({
			time,
			value: vals.reduce((s, v) => s + v, 0) / vals.length,
		}));
}

function formatPct(v: number) {
	return `${v.toFixed(1)}%`;
}
function formatBytes(v: number) {
	if (v > 1e6) return `${(v / 1e6).toFixed(1)} MB/s`;
	if (v > 1e3) return `${(v / 1e3).toFixed(1)} KB/s`;
	return `${v.toFixed(0)} B/s`;
}

const SPARKLINE_CONFIG: ChartConfig = {
	value: { color: "hsl(var(--chart-1))" },
};

function SparklineCard({
	title,
	metricName,
	formatter,
}: {
	title: string;
	metricName: string;
	formatter?: (v: number) => string;
}) {
	const { clusterTransports, workspaceId, selectedResourceIds, timeRange } =
		useObs();

	const { series, isLoading } = useQueryMetrics({
		clusterTransports,
		workspaceId,
		resourceIds: selectedResourceIds,
		timeRange,
		metricName,
		aggregation: "avg",
		enabled: clusterTransports.length > 0,
	});

	const data = useMemo(() => aggregateSeries(series), [series]);
	const latest = data.length > 0 ? data[data.length - 1].value : null;
	const fmt = formatter ?? ((v: number) => v.toFixed(2));

	return (
		<Card>
			<CardHeader className="pb-1">
				<CardTitle className="text-xs font-medium text-muted-foreground uppercase tracking-wide">
					{title}
				</CardTitle>
			</CardHeader>
			<CardContent className="pb-3 space-y-1">
				{isLoading ? (
					<div className="flex items-center gap-1.5 text-muted-foreground h-8">
						<Loader2 className="h-3.5 w-3.5 animate-spin" />
					</div>
				) : (
					<div className="text-2xl font-semibold tabular-nums">
						{latest !== null ? fmt(latest) : "—"}
					</div>
				)}
				{data.length > 1 && (
					<ChartContainer config={SPARKLINE_CONFIG} className="h-12 w-full">
						<ResponsiveContainer width="100%" height="100%">
							<AreaChart
								data={data}
								margin={{ top: 2, right: 0, bottom: 0, left: 0 }}
							>
								<XAxis dataKey="time" hide />
								<YAxis hide domain={["auto", "auto"]} />
								<Tooltip
									formatter={(v: number) => [fmt(v), title]}
									labelFormatter={(l) => new Date(l).toLocaleTimeString()}
									contentStyle={{ fontSize: 11, borderRadius: 6 }}
								/>
								<Area
									type="monotone"
									dataKey="value"
									stroke="hsl(var(--chart-1))"
									fill="hsl(var(--chart-1))"
									fillOpacity={0.15}
									strokeWidth={1.5}
									dot={false}
									isAnimationActive={false}
								/>
							</AreaChart>
						</ResponsiveContainer>
					</ChartContainer>
				)}
			</CardContent>
		</Card>
	);
}

export function ObsOverview() {
	const { isLoading, error, clusterTransports } = useObs();

	if (isLoading) {
		return (
			<div className="flex items-center justify-center py-16 gap-2 text-muted-foreground">
				<Loader2 className="h-4 w-4 animate-spin" />
				Connecting to observability endpoints…
			</div>
		);
	}

	if (error) {
		return <ErrorCard error={error} fallbackMessage="Failed to get observability access" />;
	}

	if (clusterTransports.length === 0) {
		return (
			<div className="text-sm text-muted-foreground py-8 text-center">
				No cluster observability endpoints available for this workspace.
			</div>
		);
	}

	return (
		<div className="grid grid-cols-2 md:grid-cols-4 gap-4">
			<SparklineCard
				title="CPU"
				metricName="k8s.pod.cpu_request_utilization"
				formatter={formatPct}
			/>
			<SparklineCard
				title="Memory"
				metricName="k8s.pod.memory_request_utilization"
				formatter={formatPct}
			/>
			<SparklineCard
				title="Network RX"
				metricName="k8s.pod.network.io"
				formatter={formatBytes}
			/>
			<SparklineCard
				title="Network TX"
				metricName="k8s.pod.network.io"
				formatter={formatBytes}
			/>
		</div>
	);
}
