import { useTransport } from "@connectrpc/connect-query";
import { useQueries } from "@tanstack/react-query";
import { CartesianGrid, Line, LineChart, XAxis, YAxis } from "recharts";

import { ChartContainer, ChartTooltip, ChartTooltipContent } from "@/components/design/Chart";
import { Skeleton } from "@/components/design/Skeleton";
import { metricsQueryOptions } from "@/hooks/useQueryMetrics";
import { formatPercent } from "@/lib/compute-chart";
import { formatAxisTime, timeAxis } from "@/lib/time";
import { useTimePreferences } from "@/lib/use-time-preferences";

import { formatFullTime } from "./format";
import { percentPoints, RANGE_MS, type MetricRange } from "./metric";

const PERCENT = 100;

/** One average line per region, for comparing regions at a glance. */
export function RegionAverages({
	title,
	metricName,
	regions,
	workspaceId,
	environmentId,
	resourceId,
	range,
	limit,
	format,
}: {
	title: string;
	metricName: string;
	regions: string[];
	workspaceId: string;
	environmentId: string;
	resourceId: string;
	range: MetricRange;
	limit: number | undefined;
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
	const absolute = limit !== undefined && limit > 0;
	const show = absolute ? format : formatPercent;
	const rows = new Map<number, Record<string, number>>();
	results.forEach((result, i) => {
		const region = regions[i] ?? "";
		for (const p of percentPoints(result.data ?? [], resourceId)) {
			const row = rows.get(p.time) ?? { time: p.time };
			row[region] = absolute ? (p.pct / PERCENT) * limit : p.pct;
			rows.set(p.time, row);
		}
	});
	const data = [...rows.values()].toSorted((a, b) => (a["time"] ?? 0) - (b["time"] ?? 0));
	const from = data[0]?.["time"] ?? 0;
	const to = data.at(-1)?.["time"] ?? from;
	const config = Object.fromEntries(
		regions.map((region, i) => [region, { label: region, color: `var(--series-${i.toString()})` }]),
	);
	return (
		<div className="flex min-w-0 flex-col gap-3.5">
			<div className="flex flex-wrap items-baseline gap-x-2 gap-y-1">
				<span className="text-[13.5px] font-medium">{title}</span>
				<span className="text-[12px] text-fg3">average per region</span>
				<span className="ml-auto flex flex-wrap gap-3">
					{regions.map((region, i) => (
						<span key={region} className="flex items-center gap-1.5 font-mono text-[11.5px] text-fg2">
							<span
								aria-hidden
								className="size-2 rounded-[2px] in-data-[theme=neobrutal]:ring-[1.5px] in-data-[theme=neobrutal]:ring-(--ink)"
								style={{ backgroundColor: `var(--series-${i.toString()})` }}
							/>
							{region}
						</span>
					))}
				</span>
			</div>
			{results.some((r) => r.isLoading) ? (
				<Skeleton className="h-40 w-full" />
			) : data.length === 0 ? (
				<div className="flex h-40 items-center justify-center rounded-md border border-dashed border-line2 text-sm text-fg3">
					{results.some((r) => r.error !== null) ? "Failed to load metrics" : "No data in this range"}
				</div>
			) : (
				<ChartContainer config={config} className="aspect-auto h-40 w-full" aria-label={`${title}: average per region`}>
					<LineChart data={data}>
						<CartesianGrid vertical={false} stroke="var(--line)" />
						<XAxis
							dataKey="time"
							type="number"
							domain={[from, to]}
							ticks={timeAxis(from, to, timePref)}
							interval="preserveStartEnd"
							tickFormatter={(time: number) => formatAxisTime(time, from, to, timePref)}
							tickLine={false}
							axisLine={false}
						/>
						<YAxis tickFormatter={(v: number) => show(v)} width={65} tickLine={false} axisLine={false} />
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
								dot={false}
								isAnimationActive={false}
							/>
						))}
					</LineChart>
				</ChartContainer>
			)}
		</div>
	);
}
