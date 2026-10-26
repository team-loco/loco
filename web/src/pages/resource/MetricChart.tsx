import { Area, AreaChart, CartesianGrid, ReferenceLine, XAxis, YAxis } from "recharts";

import { ChartContainer, ChartTooltip, ChartTooltipContent } from "@/components/design/Chart";
import { Skeleton } from "@/components/design/Skeleton";
import { DAY_MS } from "@/lib/time";
import { formatMetricTime, formatPercent, percentAxis, timeAxis } from "@/lib/compute-chart";

import { formatFullTime } from "./format";
import type { MetricPointPct } from "./metric";

const tooltipPercent = (value: unknown) => `${formatPercent(Number(value))} of request`;
const tooltipTime = (_label: unknown, payload: readonly { payload?: { time?: number } }[]) => {
	const time = payload[0]?.payload?.time;
	return time === undefined ? "" : formatFullTime(time);
};

const CHART_CONFIG = { pct: { label: "Utilization", color: "var(--primary)" } };

export function MetricChart({
	label,
	current,
	unit,
	points,
	isLoading,
	emptyText,
	from,
	to,
	last,
}: {
	label: string;
	current: string;
	unit: string;
	points: MetricPointPct[];
	isLoading: boolean;
	emptyText: string;
	from: number;
	to: number;
	last: boolean;
}) {
	const { maximum, ticks } = percentAxis(Math.max(0, ...points.map((p) => p.pct)));
	return (
		<div className={`flex min-w-0 flex-col gap-2 px-4 py-3.5 ${last ? "" : "border-line lg:border-r"}`}>
			<div className="flex items-baseline gap-2">
				<span className="flex-1 text-sm text-fg3">{label}</span>
				<span className="text-xl font-semibold tabular-nums">{current}</span>
			</div>
			<span className="text-xs text-fg3">{unit}</span>
			{isLoading ? (
				<Skeleton className="h-44 w-full" />
			) : points.length === 0 ? (
				<div className="flex h-44 items-center justify-center rounded-sm border border-dashed border-line text-sm text-fg3">
					{emptyText}
				</div>
			) : (
				<ChartContainer
					config={CHART_CONFIG}
					className="h-44 w-full aspect-auto"
					aria-label={`${label} percentage of requested capacity over time`}
				>
					<AreaChart data={points} margin={{ top: 8, right: to - from >= DAY_MS ? 40 : 12, bottom: 0, left: 0 }}>
						<CartesianGrid vertical={false} stroke="var(--line)" />
						<XAxis
							dataKey="time"
							type="number"
							scale="time"
							domain={[from, to]}
							ticks={timeAxis(from, to)}
							tickFormatter={(time: number) => formatMetricTime(time, from, to)}
							tickLine={false}
							axisLine={false}
							minTickGap={24}
							tick={{ fontSize: 11, fill: "var(--fg3)" }}
						/>
						<YAxis
							domain={[0, maximum]}
							ticks={ticks}
							tickFormatter={formatPercent}
							tickLine={false}
							axisLine={false}
							width={44}
							tick={{ fontSize: 11, fill: "var(--fg3)" }}
						/>
						<ReferenceLine y={100} stroke="var(--fg3)" strokeDasharray="4 4" />
						<ChartTooltip content={<ChartTooltipContent labelFormatter={tooltipTime} formatter={tooltipPercent} />} />
						<Area
							dataKey="pct"
							type="linear"
							stroke="var(--primary)"
							fill="var(--primary)"
							fillOpacity={0.08}
							strokeWidth={1.75}
							dot={points.length === 1}
							isAnimationActive={false}
						/>
					</AreaChart>
				</ChartContainer>
			)}
		</div>
	);
}
