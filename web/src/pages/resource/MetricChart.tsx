import { TriangleAlertIcon } from "lucide-react";
import { Area, ComposedChart, CartesianGrid, Line, ReferenceLine, XAxis, YAxis } from "recharts";

import { ChartContainer, ChartTooltip, ChartTooltipContent } from "@/components/design/Chart";
import { Skeleton } from "@/components/design/Skeleton";
import { DAY_MS } from "@/lib/time";
import { formatMetricTime, formatPercent, percentAxis, timeAxis } from "@/lib/compute-chart";

import { formatFullTime } from "./format";
import { scaleBand, usageSummary, type BandPoint } from "./metric";

const HEADROOM = 1.1;
const WARN_SHARE = 0.85;
const AXIS_FONT = 11;
const DAY_MARGIN = 40;
const MARGIN = 12;
const BAND_OPACITY = 0.14;
const FULL_REQUEST = 100;
const LINE_WIDTH = 1.75;

const CHART_CONFIG = {
	median: { label: "Median", color: "var(--series-0)" },
	band: { label: "Min – max", color: "var(--series-0)" },
};

const tooltipTime = (_label: unknown, payload: readonly { payload?: { time?: number } }[]) => {
	const time = payload[0]?.payload?.time;
	return time === undefined ? "" : formatFullTime(time);
};

function Stat({ label, value, warn = false }: { label: string; value: string; warn?: boolean }) {
	return (
		<div className="flex flex-col gap-0.5">
			<span className="text-[12px] text-fg3">{label}</span>
			<span className="flex items-center gap-1.5 text-[22px] leading-[1.1] font-light tracking-[-0.03em] tabular-nums">
				{value}
				{warn && <TriangleAlertIcon aria-label="Above 85% of the limit" className="size-4 shrink-0 text-warn-fg" />}
			</span>
		</div>
	);
}

export function MetricChart({
	title,
	points,
	isLoading,
	failed,
	from,
	to,
	markers,
	limit,
	format,
	replicas,
}: {
	title: string;
	points: BandPoint[];
	isLoading: boolean;
	failed: boolean;
	from: number;
	to: number;
	markers: number[];
	limit: number | undefined;
	format: (value: number) => string;
	replicas: number;
}) {
	// Without a known limit the chart falls back to percent of request.
	const absolute = limit !== undefined && limit > 0;
	const data = absolute ? scaleBand(points, limit) : points;
	const show = absolute ? format : formatPercent;
	const usage = usageSummary(data);
	const peak = Math.max(0, ...data.map((p) => p.band?.[1] ?? p.median ?? 0));
	const top = absolute ? Math.max(limit, peak) * HEADROOM : percentAxis(peak).maximum;
	const ticks = absolute ? [0, limit / 2, limit] : percentAxis(peak).ticks;
	const line = absolute ? limit : FULL_REQUEST;
	const warn = absolute && usage.highest !== undefined && usage.highest > limit * WARN_SHARE;
	const shown = markers.filter((m) => m >= from && m <= to);
	const tooltipValue = (value: unknown) =>
		Array.isArray(value) ? value.map((v) => show(Number(v))).join(" – ") : show(Number(value));
	return (
		<div className="flex min-w-0 flex-col gap-3.5">
			<div className="flex items-baseline gap-2">
				<span className="text-[13.5px] font-medium">{title}</span>
				<span className="text-[12px] text-fg3">
					band shows lowest to highest of {replicas.toString()} {replicas === 1 ? "replica" : "replicas"}
				</span>
			</div>
			<div className="flex gap-6">
				<Stat label="Average" value={usage.average === undefined ? "—" : show(usage.average)} />
				<Stat label="Highest" value={usage.highest === undefined ? "—" : show(usage.highest)} warn={warn} />
			</div>
			{isLoading ? (
				<Skeleton className="h-40 w-full" />
			) : points.length === 0 ? (
				<div className="flex h-40 items-center justify-center rounded-md border border-dashed border-line2 text-sm text-fg3">
					{failed ? "Failed to load metrics" : "No data in this range"}
				</div>
			) : (
				<ChartContainer
					config={CHART_CONFIG}
					className="aspect-auto h-40 w-full"
					aria-label={`${title}: lowest, median and highest across replicas${absolute ? "" : " as a percentage of the request"}`}
				>
					<ComposedChart
						data={data}
						margin={{ top: 8, right: to - from >= DAY_MS ? DAY_MARGIN : MARGIN, bottom: 0, left: 0 }}
					>
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
							tick={{ fontSize: AXIS_FONT, fill: "var(--fg3)" }}
						/>
						<YAxis
							domain={[0, top]}
							ticks={ticks}
							tickFormatter={show}
							tickLine={false}
							axisLine={false}
							width={absolute ? 72 : 44}
							tick={{ fontSize: AXIS_FONT, fill: "var(--fg3)" }}
						/>
						<ReferenceLine
							y={line}
							stroke="var(--fg3)"
							strokeDasharray="4 4"
							label={{
								value: `limit ${show(line)}`,
								position: "insideTopRight",
								fontSize: AXIS_FONT,
								fill: "var(--fg3)",
							}}
						/>
						{shown.map((m) => (
							<ReferenceLine key={m} x={m} stroke="var(--fg3)" strokeDasharray="2 3" />
						))}
						<ChartTooltip content={<ChartTooltipContent labelFormatter={tooltipTime} formatter={tooltipValue} />} />
						<Area
							dataKey="band"
							type="linear"
							stroke="none"
							fill="var(--series-0)"
							fillOpacity={BAND_OPACITY}
							isAnimationActive={false}
							connectNulls
						/>
						<Line
							dataKey="median"
							type="linear"
							stroke="var(--series-0)"
							strokeWidth={LINE_WIDTH}
							dot={points.length === 1}
							isAnimationActive={false}
							connectNulls
						/>
					</ComposedChart>
				</ChartContainer>
			)}
		</div>
	);
}
