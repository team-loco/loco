import { axisTimeTick } from "@/components/design/AxisTimeTick";
import { useTimePreferences } from "@/lib/use-time-preferences";
import { Area, AreaChart, CartesianGrid, XAxis, YAxis } from "recharts";

import { ChartContainer, ChartTooltip, ChartTooltipContent } from "@/components/design/Chart";
import { Section } from "@/components/design/Page";
import { Skeleton } from "@/components/design/Skeleton";
import { useTraffic } from "@/hooks/useTraffic";
import { getErrorMessage } from "@/lib/error-handler";
import { formatAxisTime, formatFullTime, timeAxis } from "@/lib/time";
import { errorShare, latestTraffic, trafficAxis, trafficParts, trafficValue, type TrafficPoint } from "@/lib/traffic";
import { RANGE_MS, type MetricRange } from "./metric";
import { markerLines } from "./MetricChart";
import { useDeploymentMarkers } from "./useDeploymentMarkers";

const ERROR_NOTE_SHARE = 2;

function TooltipRow({ label, value }: { label: string; value: string }) {
	return (
		<div className="flex justify-between gap-4">
			<span className="text-fg3">{label}</span>
			<span className="font-mono text-foreground tabular-nums">{value}</span>
		</div>
	);
}

function Stat({
	label,
	value,
	note,
}: {
	label: string;
	value: { number: string; unit: string };
	note?: string | undefined;
}) {
	return (
		<div className="flex min-w-0 flex-col gap-0.5">
			<span className="text-[12.5px] text-fg3">{label}</span>
			<span className="flex items-baseline gap-1.5">
				<span className="font-mono text-[22px] leading-[1.1] font-light tracking-[-0.03em] tabular-nums">
					{value.number}
				</span>{" "}
				{value.unit !== "" && <span className="text-[12px] whitespace-nowrap text-fg3">{value.unit.trim()}</span>}
				{note !== undefined && <span className="text-[12px] whitespace-nowrap text-warn-fg">{note}</span>}
			</span>
		</div>
	);
}

function trafficTooltip(value: unknown, _name: unknown, item: { payload?: unknown }) {
	const point = item.payload as TrafficPoint | undefined;
	return (
		<div className="grid w-full gap-1">
			<TooltipRow label="Requests" value={trafficValue(Number(value), "/s")} />
			<TooltipRow label="Errors" value={trafficValue(errorShare(point), "%")} />
			<TooltipRow label="p95" value={trafficValue(point?.latency, " ms")} />
		</div>
	);
}

export function TrafficSection({
	workspaceId,
	environmentId,
	resourceId,
	region,
	range,
	isPublic,
	nowMs,
}: {
	workspaceId: string;
	environmentId: string;
	resourceId: string;
	region: string;
	range: MetricRange;
	isPublic: boolean;
	nowMs: number;
}) {
	if (!isPublic) {
		return (
			<Section title="Traffic">
				<p className="m-0 px-5 py-4 text-[13px] text-fg3">
					Traffic is recorded for public services. This service is internal only.
				</p>
			</Section>
		);
	}
	return <PublicTraffic {...{ workspaceId, environmentId, resourceId, region, range, nowMs }} />;
}

function PublicTraffic({
	workspaceId,
	environmentId,
	resourceId,
	region,
	range,
	nowMs,
}: {
	workspaceId: string;
	environmentId: string;
	resourceId: string;
	region: string;
	range: MetricRange;
	nowMs: number;
}) {
	const timePref = useTimePreferences();
	const traffic = useTraffic({
		workspaceId,
		environmentId,
		resourceIds: [resourceId],
		region,
		spanMs: RANGE_MS[range],
	});
	const markers = useDeploymentMarkers({ workspaceId, environmentId, resourceId, range, nowMs });
	const to = traffic.endMs;
	const from = to - RANGE_MS[range];
	const latest = latestTraffic(traffic.points, resourceId);
	const share = errorShare(latest);
	const axis = trafficAxis(Math.max(0, ...traffic.points.map((point) => point.requests ?? 0)), "/s");
	return (
		<Section title="Traffic">
			{traffic.error !== null ? (
				<div className="p-5">
					<p
						role="alert"
						className="m-0 flex h-32 items-center justify-center rounded-md border border-dashed border-line2 text-sm text-fg3"
					>
						{getErrorMessage(traffic.error, "Failed to load traffic")}
					</p>
				</div>
			) : (
				<div className="flex min-w-0 flex-col gap-3.5 px-5 pt-[18px] pb-3">
					<div className="flex flex-wrap gap-7">
						<Stat label="Requests" value={trafficParts(latest?.requests, " req/s")} />
						<Stat
							label="Errors"
							value={trafficParts(share, "%")}
							note={
								share !== undefined && share > ERROR_NOTE_SHARE ? `above ${ERROR_NOTE_SHARE.toString()}%` : undefined
							}
						/>
						<Stat label="p95" value={trafficParts(latest?.latency, " ms")} />
					</div>
					{traffic.isLoading ? (
						<Skeleton className="h-[150px] w-full" />
					) : traffic.points.some((point) => point.requests !== undefined) ? (
						<ChartContainer
							config={{ requests: { label: "Requests", color: "var(--series-0)" } }}
							className="aspect-auto h-[150px] w-full"
							aria-label="Requests"
						>
							<AreaChart data={traffic.points}>
								<CartesianGrid vertical={false} stroke="var(--line)" />
								<XAxis
									dataKey="time"
									type="number"
									domain={[from, to]}
									allowDataOverflow
									ticks={timeAxis(from, to, timePref)}
									interval="preserveStartEnd"
									tickFormatter={(time: number) => formatAxisTime(time, from, to, timePref)}
									tick={axisTimeTick(from, to, timePref)}
									tickLine={false}
									axisLine={false}
								/>
								<YAxis
									domain={[0, axis.top]}
									ticks={axis.ticks}
									tickFormatter={axis.format}
									width={65}
									tickLine={false}
									axisLine={false}
								/>
								<ChartTooltip
									content={
										<ChartTooltipContent
											labelFormatter={(_label, payload) => {
												const point = payload[0]?.payload as { time?: number } | undefined;
												return point?.time === undefined ? "" : formatFullTime(point.time, false, timePref);
											}}
											formatter={trafficTooltip}
										/>
									}
								/>
								{markerLines(markers, from, to)}
								<Area
									dataKey="requests"
									stroke="var(--series-0)"
									fill="var(--series-0)"
									fillOpacity={0.12}
									dot={false}
									isAnimationActive={false}
								/>
							</AreaChart>
						</ChartContainer>
					) : (
						<p className="flex h-[150px] items-center justify-center text-sm text-fg3">No data in this range</p>
					)}
				</div>
			)}
		</Section>
	);
}
