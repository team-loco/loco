import { axisTimeTick } from "@/components/design/AxisTimeTick";
import { useTransport } from "@connectrpc/connect-query";
import { useQueries } from "@tanstack/react-query";
import { CartesianGrid, ComposedChart, Line, ReferenceLine, XAxis, YAxis } from "recharts";

import { ChartContainer, ChartTooltip, ChartTooltipContent } from "@/components/design/Chart";
import { Skeleton } from "@/components/design/Skeleton";
import { metricsQueryOptions } from "@/hooks/useQueryMetrics";
import { formatPercent, limitAxis, percentAxis } from "@/lib/compute-chart";
import { limitAt, type LimitStep } from "@/lib/limit-history";
import { formatAxisTime, timeAxis } from "@/lib/time";
import { useTimePreferences } from "@/lib/use-time-preferences";

import { formatFullTime } from "./format";
import { percentPoints, RANGE_MS, type ChartMarker, type MetricRange } from "./metric";

const PERCENT = 100;
const AXIS_FONT = 11;
const FULL_REQUEST = 100;

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
	const axis = absolute ? limitAxis(Math.max(limit, ...data.map((row) => row["limit"] ?? 0)), peak) : percentAxis(peak);
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
					<ComposedChart data={data} margin={{ top: 8, right: 4, bottom: 0, left: 0 }}>
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
						<YAxis
							domain={[0, axis.maximum]}
							ticks={axis.ticks}
							tickFormatter={show}
							width={absolute ? 72 : 44}
							tick={{ fontSize: AXIS_FONT, fill: "var(--fg3)" }}
							tickLine={false}
							axisLine={false}
						/>
						{resized ? (
							<Line
								dataKey="limit"
								type="stepAfter"
								stroke="var(--fg3)"
								strokeDasharray="4 4"
								strokeWidth={1}
								dot={false}
								isAnimationActive={false}
							/>
						) : (
							<ReferenceLine
								y={limit}
								stroke="var(--fg3)"
								strokeDasharray="4 4"
								label={{
									value: `limit ${show(limit)}`,
									position: "insideTopRight",
									fontSize: AXIS_FONT,
									fill: "var(--fg3)",
								}}
							/>
						)}
						{markers
							.filter((m) => m.ms >= from && m.ms <= to)
							.map((m) => (
								<ReferenceLine
									key={m.ms}
									x={m.ms}
									stroke="var(--fg3)"
									strokeDasharray="2 3"
									label={{ value: m.label, position: "insideTopLeft", fontSize: AXIS_FONT, fill: "var(--fg3)" }}
								/>
							))}
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
								dot={data.length === 1}
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
