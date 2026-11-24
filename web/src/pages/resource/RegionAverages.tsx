import { loneSampleDot } from "@/lib/chart-dot";
import { axisTimeTick } from "@/components/design/AxisTimeTick";
import { useTransport } from "@connectrpc/connect-query";
import { useQueries } from "@tanstack/react-query";
import { CartesianGrid, ComposedChart, Line, usePlotArea, XAxis, YAxis } from "recharts";

import { ChartContainer, ChartTooltip, ChartTooltipContent } from "@/components/design/Chart";
import { Skeleton } from "@/components/design/Skeleton";
import { metricsQueryOptions } from "@/hooks/useQueryMetrics";
import { formatPercent } from "@/lib/compute-chart";
import { limitAt, type LimitStep } from "@/lib/limit-history";
import { formatAxisTime, timeAxis } from "@/lib/time";
import { useTimePreferences } from "@/lib/use-time-preferences";

import { chartMax, limitLabelX, placeMarkers } from "./chart-layout";
import { formatFullTime } from "./format";
import { percentPoints, RANGE_MS, type ChartMarker, type MetricRange } from "./metric";

const PERCENT = 100;
const AXIS_FONT = 11;
const FULL_REQUEST = 100;
const CHAR_PX = 6;
const MARKER_BAND = 14;
const LIMIT_LABEL_X = 40;
const LABEL_HEIGHT = 13;
const NUMBER_HEIGHT = 14;
const GRID_QUARTERS = [0, 0.25, 0.5, 0.75];
const ERR_LINE = "color-mix(in oklch, var(--err) 55%, transparent)";
const ERR_LABEL = "color-mix(in oklch, var(--err) 80%, var(--fg2))";

/** How a chart's values read on its axis: the divisor into the unit, the unit, and the digits shown. */
export interface AxisScale {
	divisor: number;
	unit: string;
	decimals: number;
}

function axisNumber(value: number, scale: AxisScale): string {
	return (value / scale.divisor).toFixed(scale.decimals);
}

/** Axis numbers, the limit and the event markers, drawn inside the plot so they never share a row. */
function ChartAnnotations({
	from,
	to,
	max,
	scale,
	limit,
	limitLabel,
	showLimit,
	markers,
}: {
	from: number;
	to: number;
	max: number;
	scale: AxisScale;
	limit: number;
	limitLabel: string;
	showLimit: boolean;
	markers: readonly ChartMarker[];
}) {
	const area = usePlotArea();
	if (area === undefined) return null;
	const yAt = (value: number) => area.y + area.height * (1 - value / max);
	const xAt = (ms: number) => area.x + ((ms - from) / Math.max(1, to - from)) * area.width;
	const inRange = markers.filter((m) => m.ms >= from && m.ms <= to).map((m) => ({ x: xAt(m.ms), label: m.label }));
	const placed = placeMarkers(inRange, area.x, area.x + area.width, CHAR_PX);
	const limitY = yAt(limit);
	const labelWidth = limitLabel.length * CHAR_PX + 6;
	const labelX = limitLabelX(
		area.x + LIMIT_LABEL_X,
		labelWidth,
		inRange.map((m) => m.x),
		area.x + area.width,
	);
	const numbers = GRID_QUARTERS.filter((q) => {
		const tickY = yAt(max * q);
		return limitY < tickY - NUMBER_HEIGHT || limitY > tickY + 1;
	});
	return (
		<g pointerEvents="none" fontFamily="var(--font-mono)" fontSize={10}>
			{inRange.map((m) => (
				<line
					key={m.x}
					x1={m.x}
					x2={m.x}
					y1={area.y - MARKER_BAND}
					y2={area.y + area.height}
					stroke="var(--fg4)"
					strokeDasharray="3 3"
				/>
			))}
			{placed.map((m) => (
				<g key={m.x}>
					<rect
						x={m.textX - 3}
						y={area.y - MARKER_BAND}
						width={m.label.length * CHAR_PX + 6}
						height={LABEL_HEIGHT}
						rx={3}
						fill="var(--panel)"
					/>
					<text x={m.textX} y={area.y - MARKER_BAND + 10} fill="var(--fg3)">
						{m.label}
					</text>
				</g>
			))}
			{numbers.map((q) => (
				<text key={q} x={area.x + 2} y={yAt(max * q) - 4} fill="var(--fg4)">
					{axisNumber(max * q, scale)}
				</text>
			))}
			{showLimit && (
				<line x1={area.x} x2={area.x + area.width} y1={limitY} y2={limitY} stroke={ERR_LINE} strokeDasharray="4 4" />
			)}
			<rect
				x={labelX - 3}
				y={limitY - LABEL_HEIGHT - 2}
				width={labelWidth}
				height={LABEL_HEIGHT}
				rx={3}
				fill="var(--panel)"
			/>
			<text x={labelX} y={limitY - 6} fill={ERR_LABEL}>
				{limitLabel}
			</text>
		</g>
	);
}

/** One average line per region, with the limit as a dashed line. */
export function RegionAverages({
	title,
	metricName,
	regions,
	workspaceId,
	environmentId,
	resourceId,
	range,
	limits,
	markers,
	format,
	scale,
}: {
	title: string;
	metricName: string;
	regions: string[];
	workspaceId: string;
	environmentId: string;
	resourceId: string;
	range: MetricRange;
	limits: Map<string, LimitStep[]> | undefined;
	markers: readonly ChartMarker[];
	format: (value: number) => string;
	/** The unit the axis and the limit label read in, given the limit. */
	scale: (limit: number) => AxisScale;
}) {
	const timePref = useTimePreferences();
	const transport = useTransport();
	const results = useQueries({
		queries: regions.map((region) =>
			metricsQueryOptions(transport, {
				workspaceId,
				resourceIds: [resourceId],
				spanMs: RANGE_MS[range],
				metricName,
				environmentId,
				region,
				aggregation: "avg",
			}),
		),
	});
	const loading = results.some((r) => r.isLoading);
	const absolute = limits !== undefined;
	const show = absolute ? format : formatPercent;
	const limitSteps = limits?.get(regions[0] ?? "") ?? [];
	const rows = new Map<number, Record<string, number>>();
	results.forEach((result, i) => {
		const region = regions[i] ?? "";
		for (const p of percentPoints(result.data ?? [], resourceId)) {
			const row = rows.get(p.time) ?? {
				time: p.time,
				limit: absolute ? (limitAt(limitSteps, p.time) ?? 0) : FULL_REQUEST,
			};
			row[region] = absolute ? (p.pct / PERCENT) * (limitAt(limits.get(region) ?? [], p.time) ?? 0) : p.pct;
			rows.set(p.time, row);
		}
	});
	const data = [...rows.values()].toSorted((a, b) => (a["time"] ?? 0) - (b["time"] ?? 0));
	const to = Math.max(0, ...results.map((r) => r.dataUpdatedAt));
	const from = to - RANGE_MS[range];
	const limit = data.at(-1)?.["limit"] ?? FULL_REQUEST;
	const resized = data.some((row) => row["limit"] !== limit);
	const peak = Math.max(0, ...data.flatMap((row) => regions.map((region) => row[region] ?? 0)));
	const top = Math.max(limit, ...data.map((row) => row["limit"] ?? 0));
	const axisScale = absolute ? scale(limit) : { divisor: 1, unit: "%", decimals: 0 };
	const max = chartMax(peak / axisScale.divisor, top / axisScale.divisor) * axisScale.divisor;
	const limitNumber = String(Number(axisNumber(limit, axisScale)));
	const limitLabel = `limit ${limitNumber}${axisScale.unit === "%" ? "%" : ` ${axisScale.unit}`}`;
	const latest = (region: string) => data.findLast((row) => row[region] !== undefined)?.[region];
	const config = Object.fromEntries(
		regions.map((region, i) => [region, { label: region, color: `var(--series-${i.toString()})` }]),
	);
	return (
		<div className="flex min-w-0 flex-col gap-2.5">
			<div className="flex items-center gap-2">
				<span className="text-[14px] font-medium">{title}</span>
				<span className="text-[12px] text-fg3">{regions.length > 1 ? "average per region" : "average"}</span>
			</div>
			<div className="flex flex-wrap gap-x-[22px] gap-y-1.5 text-[12.5px] text-fg2">
				{regions.map((region, i) => {
					const value = latest(region);
					return (
						<span key={region} className="inline-flex items-center gap-1.5">
							<span
								aria-hidden
								className="size-2 rounded-[2px] in-data-[theme=neobrutal]:ring-[1.5px] in-data-[theme=neobrutal]:ring-(--ink)"
								style={{ backgroundColor: `var(--series-${i.toString()})` }}
							/>
							<span className="font-mono">{region}</span>
							{!loading && (
								<span className="ml-1.5 font-mono text-[12px] text-foreground">
									{value === undefined ? "—" : show(value)}
								</span>
							)}
						</span>
					);
				})}
			</div>
			{loading ? (
				<Skeleton className="h-[170px] w-full" />
			) : data.length === 0 ? (
				<div className="flex h-[170px] items-center justify-center rounded-md border border-dashed border-line2 text-sm text-fg3">
					{results.some((r) => r.error !== null) ? "Failed to load metrics" : "No data in this range"}
				</div>
			) : (
				<ChartContainer
					config={config}
					className="aspect-auto h-[170px] w-full"
					aria-label={`${title}: ${regions.length > 1 ? "average per region" : "average"}`}
				>
					<ComposedChart data={data} margin={{ top: MARKER_BAND, right: 4, bottom: 0, left: 0 }}>
						<CartesianGrid vertical={false} stroke="var(--line)" />
						<XAxis
							dataKey="time"
							type="number"
							scale="time"
							domain={[from, to]}
							allowDataOverflow
							ticks={timeAxis(from, to, timePref)}
							interval="preserveStartEnd"
							minTickGap={24}
							tickFormatter={(time: number) => formatAxisTime(time, from, to, timePref)}
							tick={axisTimeTick(from, to, timePref, AXIS_FONT)}
							tickLine={false}
							axisLine={false}
						/>
						<YAxis hide domain={[0, max]} ticks={GRID_QUARTERS.map((q) => max * q)} />
						{resized && (
							<Line
								dataKey="limit"
								type="stepAfter"
								stroke={ERR_LINE}
								strokeDasharray="4 4"
								strokeWidth={1}
								dot={false}
								isAnimationActive={false}
							/>
						)}
						<ChartAnnotations
							from={from}
							to={to}
							max={max}
							scale={axisScale}
							limit={limit}
							limitLabel={limitLabel}
							showLimit={!resized}
							markers={markers}
						/>
						<ChartTooltip
							content={
								<ChartTooltipContent
									formatter={(value, name) => `${String(name)} ${show(Number(value))}`}
									labelFormatter={(_label, payload) => {
										const time = (payload[0]?.payload as { time?: number } | undefined)?.time;
										return time === undefined ? "" : formatFullTime(time, false, timePref);
									}}
								/>
							}
						/>
						{regions.map((region, i) => (
							<Line
								key={region}
								dataKey={region}
								stroke={`var(--series-${i.toString()})`}
								strokeWidth={1.75}
								dot={loneSampleDot(
									data.filter((row) => row[region] !== undefined).length,
									`var(--series-${i.toString()})`,
								)}
								isAnimationActive={false}
								connectNulls
							/>
						))}
					</ComposedChart>
				</ChartContainer>
			)}
		</div>
	);
}
