import { useMemo } from "react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { ChartContainer, type ChartConfig } from "@/components/ui/chart";
import { Loader2, TrendingUp } from "lucide-react";
import {
	AreaChart,
	Area,
	XAxis,
	YAxis,
	CartesianGrid,
	Tooltip,
	ResponsiveContainer,
} from "recharts";
import type { MetricSeries } from "@/gen/loco/observability/v1/observability_pb";
import { useObs } from "./ObsProvider";
import { useQueryMetrics } from "@/hooks/useQueryMetrics";

interface ChartDataPoint {
	time: number;
	[key: string]: number;
}

function formatValue(value: number, unit: string): string {
	if (unit === "%") return `${value.toFixed(1)}%`;
	if (unit === "bytes/s") {
		if (value > 1e6) return `${(value / 1e6).toFixed(1)} MB/s`;
		if (value > 1e3) return `${(value / 1e3).toFixed(1)} KB/s`;
		return `${value.toFixed(0)} B/s`;
	}
	return value.toFixed(2);
}

function buildChartData(series: MetricSeries[]): ChartDataPoint[] {
	if (series.length === 0) return [];

	// Collect all timestamps
	const tsSet = new Set<number>();
	for (const s of series) {
		for (const pt of s.points) {
			if (pt.timestamp) tsSet.add(Number(pt.timestamp.seconds) * 1000);
		}
	}
	const timestamps = Array.from(tsSet).sort((a, b) => a - b);

	return timestamps.map((ts) => {
		const pt: ChartDataPoint = { time: ts };
		for (const s of series) {
			const key = s.resourceId || s.labels["k8s.pod.name"] || "value";
			const match = s.points.find(
				(p) => p.timestamp && Number(p.timestamp.seconds) * 1000 === ts,
			);
			pt[key] = match?.value ?? 0;
		}
		return pt;
	});
}

const CHART_COLORS = [
	"hsl(var(--chart-1))",
	"hsl(var(--chart-2))",
	"hsl(var(--chart-3))",
	"hsl(var(--chart-4))",
	"hsl(var(--chart-5))",
];

interface ObsMetricChartProps {
	title: string;
	metricName: string;
	unit?: string;
	aggregation?: string;
	yDomain?: [number, number];
}

export function ObsMetricChart({
	title,
	metricName,
	unit = "",
	aggregation = "avg",
	yDomain,
}: ObsMetricChartProps) {
	const { clusterTransports, workspaceId, selectedResourceIds, timeRange } =
		useObs();

	const { series, isLoading, error } = useQueryMetrics({
		clusterTransports,
		workspaceId,
		resourceIds: selectedResourceIds,
		timeRange,
		metricName,
		aggregation,
		enabled: clusterTransports.length > 0,
	});

	const chartData = useMemo(() => buildChartData(series), [series]);

	const seriesKeys = useMemo(
		() =>
			series.map(
				(s) => s.resourceId || s.labels["k8s.pod.name"] || "value",
			),
		[series],
	);

	const chartConfig = useMemo(() => {
		const cfg: ChartConfig = {};
		seriesKeys.forEach((key, i) => {
			cfg[key] = {
				label: key,
				color: CHART_COLORS[i % CHART_COLORS.length],
			};
		});
		return cfg;
	}, [seriesKeys]);

	return (
		<Card>
			<CardHeader className="pb-2">
				<CardTitle className="text-sm font-medium flex items-center gap-1.5">
					<TrendingUp className="h-3.5 w-3.5 text-muted-foreground" />
					{title}
				</CardTitle>
			</CardHeader>
			<CardContent className="pb-3">
				{isLoading && (
					<div className="flex items-center justify-center h-32 text-muted-foreground">
						<Loader2 className="h-4 w-4 animate-spin" />
					</div>
				)}
				{error && (
					<div className="flex items-center justify-center h-32 text-xs text-destructive">
						{error.message}
					</div>
				)}
				{!isLoading && !error && chartData.length === 0 && (
					<div className="flex items-center justify-center h-32 text-xs text-muted-foreground">
						No data
					</div>
				)}
				{!isLoading && !error && chartData.length > 0 && (
					<ChartContainer config={chartConfig} className="h-32 w-full">
						<ResponsiveContainer width="100%" height="100%">
							<AreaChart
								data={chartData}
								margin={{ top: 4, right: 4, bottom: 0, left: 0 }}
							>
								<CartesianGrid
									strokeDasharray="3 3"
									stroke="hsl(var(--border))"
									opacity={0.5}
								/>
								<XAxis
									dataKey="time"
									tickFormatter={(v) =>
										new Date(v).toLocaleTimeString("en-US", {
											hour: "2-digit",
											minute: "2-digit",
											hour12: false,
										})
									}
									tick={{ fontSize: 10 }}
									tickLine={false}
									axisLine={false}
									minTickGap={40}
								/>
								<YAxis
									domain={yDomain ?? ["auto", "auto"]}
									tickFormatter={(v) => formatValue(v, unit)}
									tick={{ fontSize: 10 }}
									tickLine={false}
									axisLine={false}
									width={40}
								/>
								<Tooltip
									formatter={(value) => [
										formatValue(Number(value), unit),
										undefined,
									]}
									labelFormatter={(label) =>
										new Date(label).toLocaleString()
									}
									contentStyle={{
										fontSize: 11,
										borderRadius: 6,
									}}
								/>
								{seriesKeys.map((key, i) => (
									<Area
										key={key}
										type="monotone"
										dataKey={key}
										stroke={CHART_COLORS[i % CHART_COLORS.length]}
										fill={CHART_COLORS[i % CHART_COLORS.length]}
										fillOpacity={0.15}
										strokeWidth={1.5}
										dot={false}
										isAnimationActive={false}
									/>
								))}
							</AreaChart>
						</ResponsiveContainer>
					</ChartContainer>
				)}
			</CardContent>
		</Card>
	);
}
