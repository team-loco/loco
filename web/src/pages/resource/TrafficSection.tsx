import { CartesianGrid, Line, LineChart, XAxis, YAxis } from "recharts";

import { ChartContainer, ChartTooltip, ChartTooltipContent } from "@/components/design/Chart";
import { Section } from "@/components/design/Page";
import { Skeleton } from "@/components/design/Skeleton";
import { useTraffic } from "@/hooks/useTraffic";
import { getErrorMessage } from "@/lib/error-handler";
import { formatHourMinute } from "@/lib/time";
import { errorShare, latestTraffic, trafficValue } from "@/lib/traffic";
import { RANGE_MS, type MetricRange } from "./metric";

const CHARTS = [
	{ key: "requests", title: "Requests", unit: "/s" },
	{ key: "share", title: "5xx", unit: "%" },
	{ key: "bytes", title: "Bandwidth out", unit: " B/s" },
	{ key: "latency", title: "p95 latency", unit: " ms" },
] as const;

export function TrafficSection({
	workspaceId,
	environmentId,
	resourceId,
	region,
	range,
}: {
	workspaceId: string;
	environmentId: string;
	resourceId: string;
	region: string;
	range: MetricRange;
}) {
	const traffic = useTraffic({
		workspaceId,
		environmentId,
		resourceIds: [resourceId],
		region,
		spanMs: RANGE_MS[range],
	});
	const points = traffic.points.map((point) => ({ ...point, share: errorShare(point) }));
	const latest = latestTraffic(traffic.points, resourceId);
	return (
		<Section title="Traffic">
			{traffic.error !== null ? (
				<p role="alert" className="m-0 px-5 py-4 text-bad-fg">
					{getErrorMessage(traffic.error, "Failed to load traffic")}
				</p>
			) : (
				<div className="grid gap-6 p-5 sm:grid-cols-2">
					{CHARTS.map(({ key, title, unit }) => (
						<div key={key} className="min-w-0">
							<div className="flex items-baseline justify-between gap-2 text-[13px]">
								<span>{title}</span>
								<span className="font-mono">
									{trafficValue(key === "share" ? errorShare(latest) : latest?.[key], unit)}
								</span>
							</div>
							{traffic.isLoading ? (
								<Skeleton className="mt-3 h-32 w-full" />
							) : points.some((point) => point[key] !== undefined) ? (
								<ChartContainer
									config={{ [key]: { label: title, color: "var(--series-0)" } }}
									className="mt-3 aspect-auto h-32 w-full"
									aria-label={title}
								>
									<LineChart data={points}>
										<CartesianGrid vertical={false} stroke="var(--line)" />
										<XAxis
											dataKey="time"
											tickFormatter={(time: number) => formatHourMinute(time)}
											tickLine={false}
											axisLine={false}
										/>
										<YAxis
											tickFormatter={(value: number) => trafficValue(value, unit)}
											width={65}
											tickLine={false}
											axisLine={false}
										/>
										<ChartTooltip
											content={
												<ChartTooltipContent
													labelFormatter={(_label, payload) => {
														const point = payload[0]?.payload as { time?: number } | undefined;
														return point?.time === undefined ? "" : new Date(point.time).toLocaleString();
													}}
												/>
											}
										/>
										<Line dataKey={key} stroke="var(--series-0)" dot={false} isAnimationActive={false} />
									</LineChart>
								</ChartContainer>
							) : (
								<p className="flex h-32 items-center justify-center text-sm text-fg3">No data in this range</p>
							)}
						</div>
					))}
				</div>
			)}
		</Section>
	);
}
