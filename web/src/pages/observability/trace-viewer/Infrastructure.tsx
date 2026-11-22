import { useMemo, useState } from "react";
import type { TraceSpan } from "@gen/loco/observability/v1/observability_pb";
import { useQueries } from "@tanstack/react-query";
import { useTransport } from "@connectrpc/connect-query";
import { Area, CartesianGrid, ComposedChart, Line, ReferenceArea, ReferenceLine, XAxis, YAxis } from "recharts";
import { ChartContainer, ChartTooltip } from "@/components/design/Chart";
import { Spinner } from "@/components/design/Spinner";
import { useEnvironments } from "@/hooks/useEnvironment";
import { metricsQueryOptions } from "@/hooks/useQueryMetrics";
import { formatClock, formatHourMinute, timeAxis } from "@/lib/time";
import { useTimePreferences } from "@/lib/use-time-preferences";
import { getErrorMessage } from "@/lib/error-handler";
import { limitAxis } from "@/lib/compute-chart";
import { BYTES_PER_MI, formatCpu, formatMemory, MILLI_PER_CORE } from "@/lib/quantity";
import { trafficValue } from "@/lib/traffic";
import { cn } from "@/lib/utils";
import { formatDuration } from "@/pages/resource/format";
import { POD_LABEL } from "@/pages/resource/replicas";
import { useObs } from "../context";
import type { EventItem } from "../events";
import type { TraceSummary } from "../traces";
import { traceBounds } from "./trace-time";
import { limitOf, networkPoints, sampleAt, valuePoints, type InfraPoint } from "./infra-series";
import { SpanTipTrigger } from "./SpanTip";
import { WindowToggle } from "./TraceEvents";

const METRICS = [
	{ metric: "k8s.pod.cpu.usage", aggregation: "avg", direction: "" },
	{ metric: "k8s.pod.cpu_limit_utilization", aggregation: "avg", direction: "" },
	{ metric: "k8s.pod.memory.working_set", aggregation: "avg", direction: "" },
	{ metric: "k8s.pod.memory.working_set_limit_utilization", aggregation: "avg", direction: "" },
	{ metric: "k8s.pod.network.io", aggregation: "rate", direction: "receive" },
	{ metric: "k8s.pod.network.io", aggregation: "rate", direction: "transmit" },
];
// Usage past this share of the limit marks the chart as running hot.
const HOT_SHARE = 0.85;
const AXIS_WIDTH = 72;
const PLOT_TOP = 8;
const PLOT_RIGHT = 8;
const X_AXIS_HEIGHT = 30;
const networkLabel = (point: InfraPoint | undefined) =>
	`${trafficValue(point?.value, "")} / ${trafficValue(point?.out, "")} B/s`;
const CHART_CONFIG = {
	value: { label: "Usage", color: "var(--series-0)" },
	out: { label: "Out", color: "var(--series-2)" },
};

export function Infrastructure({
	trace,
	selected,
	events,
	moreEvents,
	minutes,
	onMinutes,
}: {
	trace: TraceSummary;
	selected: TraceSpan;
	events: EventItem[];
	moreEvents: boolean;
	minutes: number;
	onMinutes: (minutes: number) => void;
}) {
	const timePref = useTimePreferences();
	const { workspaceId } = useObs();
	const { active } = useEnvironments();
	const transport = useTransport();
	const [hoverTime, setHoverTime] = useState<number | null>(null);
	const pod = selected.resourceAttributes[POD_LABEL] ?? "";
	const { start, end } = traceBounds(trace);
	const from = start - minutes * 60_000;
	const to = end + minutes * 60_000;
	const queries = useQueries({
		queries: METRICS.map((metric) =>
			metricsQueryOptions(transport, {
				workspaceId,
				environmentId: active?.id ?? "",
				resourceIds: [selected.resourceId],
				spanMs: to - from,
				endMs: to,
				metricName: metric.metric,
				aggregation: metric.aggregation,
				attributes: metric.direction === "" ? {} : { direction: metric.direction },
				byPod: true,
				region: selected.resourceAttributes["loco.region"] ?? "",
				enabled: pod !== "" && selected.resourceId !== "",
			}),
		),
	});
	const [cpu, cpuShare, memory, memoryShare, incoming, outgoing] = queries.map((query) =>
		query.data?.filter((series) => series.labels[POD_LABEL] === pod),
	);
	const points = useMemo(() => {
		const id = selected.resourceId;
		const cpuPoints = valuePoints(cpu, id);
		const memoryPoints = valuePoints(memory, id);
		return {
			cpu: cpuPoints,
			cpuLimit: limitOf(cpuPoints, valuePoints(cpuShare, id)),
			memory: memoryPoints,
			memoryLimit: limitOf(memoryPoints, valuePoints(memoryShare, id)),
			network: networkPoints(incoming, outgoing, id),
		};
	}, [cpu, cpuShare, memory, memoryShare, incoming, outgoing, selected.resourceId]);
	const marks = events.filter(
		(event) => event.resourceId === selected.resourceId && event.ts >= from && event.ts <= to,
	);
	const started = Date.parse(selected.resourceAttributes["k8s.pod.start_time"] ?? "");
	const uptime = Number.isFinite(started) && started <= start ? formatDuration(start - started) : null;
	const panels = [
		{
			title: "CPU",
			indices: [0, 1],
			color: "var(--series-0)",
			data: points.cpu,
			limit: points.cpuLimit,
			fmt: (value: number) => formatCpu(value * MILLI_PER_CORE),
		},
		{
			title: "Memory",
			indices: [2, 3],
			color: "var(--series-1)",
			data: points.memory,
			limit: points.memoryLimit,
			fmt: (value: number) => formatMemory(value / BYTES_PER_MI),
		},
		{
			title: "Network",
			indices: [4, 5],
			color: "var(--series-2)",
			data: points.network,
			limit: undefined,
			fmt: (value: number) => trafficValue(value, "B/s"),
		},
	];
	return (
		<section aria-label="Trace infrastructure" className="flex min-w-0 flex-col gap-3 px-5 py-3">
			<div className="flex flex-wrap items-center gap-2">
				<WindowToggle
					label="Infrastructure window"
					minutes={minutes}
					onMinutes={(next) => {
						onMinutes(next);
						setHoverTime(null);
					}}
				/>
				<span className="ml-auto text-[12px] text-fg3">
					{uptime === null ? "Uptime not recorded" : `Up ${uptime} when this ran`}
				</span>
			</div>
			{pod === "" ? (
				<p className="m-0 text-[12px] text-fg3">No replica identity was recorded for this span.</p>
			) : (
				<div className="grid min-w-0 gap-4 sm:grid-cols-3">
					{panels.map((panel) => {
						const { data, limit, fmt } = panel;
						const network = panel.title === "Network";
						const sample = sampleAt(data, hoverTime ?? start);
						const peak = data.reduce<number | undefined>(
							(max, point) =>
								point.value === undefined ? max : max === undefined ? point.value : Math.max(max, point.value),
							undefined,
						);
						const axis = limit === undefined ? undefined : limitAxis(limit, peak ?? 0);
						const hot = limit !== undefined && peak !== undefined && peak > limit * HOT_SHARE;
						const error = panel.indices.map((i) => queries[i]?.error).find((e) => e !== null && e !== undefined);
						const loading = panel.indices.some((i) => queries[i]?.isLoading === true);
						return (
							<section key={panel.title} aria-label={`${panel.title} for ${pod}`} className="min-w-0">
								<div className="flex min-h-5 items-baseline gap-2">
									<h4 className="m-0 text-[12.5px] font-normal text-fg2">{panel.title}</h4>
									<output
										aria-live="off"
										aria-label={`${panel.title} at ${hoverTime === null ? "trace start" : "cursor"}`}
										className="min-w-0 truncate font-mono text-[13px] text-foreground"
									>
										{network ? networkLabel(sample) : sample?.value === undefined ? "—" : fmt(sample.value)}
										{hoverTime !== null && (
											<span className="text-[11.5px] text-fg3">{` at ${formatHourMinute(hoverTime, timePref)}`}</span>
										)}
									</output>
									{network ? (
										<span className="ml-auto inline-flex shrink-0 items-center gap-1 text-[11.5px] text-fg3">
											<i aria-hidden className="w-2 border-t-2" style={{ borderColor: panel.color }} />
											in
											<i
												aria-hidden
												className="ml-2 w-2 border-t-2 border-dashed"
												style={{ borderColor: panel.color }}
											/>
											out
										</span>
									) : (
										peak !== undefined && (
											<span className="ml-auto shrink-0 text-[11.5px] whitespace-nowrap text-fg3">
												peak <span className={cn("font-mono text-fg2", hot && "text-bad-fg")}>{fmt(peak)}</span>
												{hot && <span className="sr-only">, near the limit</span>}
											</span>
										)
									)}
								</div>
								{loading ? (
									<div role="status" className="mt-1 flex h-36 items-center justify-center gap-2 text-fg3">
										<Spinner />
										Loading metrics…
									</div>
								) : error !== undefined ? (
									<p role="alert" className="m-0 mt-1 flex h-36 items-center text-[12px] text-bad-fg">
										{getErrorMessage(error, "Couldn’t load replica metrics")}
									</p>
								) : data.length === 0 ? (
									<div className="mt-1 flex h-36 items-center justify-center rounded-md border border-dashed border-line text-[12px] text-fg3">
										No metrics in this range
									</div>
								) : (
									<div className="relative mt-1">
										<ChartContainer
											role="group"
											aria-label={`${panel.title} usage`}
											config={CHART_CONFIG}
											className="tv-infra-chart h-36 w-full aspect-auto"
										>
											<ComposedChart
												data={data}
												margin={{ top: PLOT_TOP, right: PLOT_RIGHT, bottom: 0, left: 0 }}
												onMouseMove={(state) => {
													const time = Number(state.activeLabel);
													if (Number.isFinite(time)) setHoverTime(time);
												}}
												onMouseLeave={() => {
													setHoverTime(null);
												}}
											>
												<CartesianGrid vertical={false} stroke="var(--line)" />
												<XAxis
													dataKey="time"
													type="number"
													domain={[from, to]}
													ticks={timeAxis(from, to, timePref)}
													tickFormatter={(time: number) => formatHourMinute(time, timePref)}
													tickLine={false}
													axisLine={false}
													minTickGap={30}
													height={X_AXIS_HEIGHT}
													allowDataOverflow
												/>
												<YAxis
													{...(axis === undefined
														? { domain: [0, "auto"] }
														: { domain: [0, axis.maximum], ticks: axis.ticks })}
													tickFormatter={fmt}
													tickLine={false}
													axisLine={false}
													width={AXIS_WIDTH}
												/>
												<ReferenceArea x1={start} x2={end} fill="var(--fg3)" fillOpacity={0.18} />
												{limit !== undefined && (
													<ReferenceLine
														y={limit}
														stroke="var(--err)"
														strokeDasharray="4 3"
														strokeOpacity={0.6}
														label={{
															value: `limit ${fmt(limit)}`,
															fill: "var(--err)",
															fontSize: 10.5,
															position: "insideBottomRight",
														}}
													/>
												)}
												{hoverTime !== null && <ReferenceLine x={hoverTime} stroke="var(--fg3)" />}
												<ChartTooltip cursor={false} content={() => null} />
												{network ? (
													<>
														<Line
															dataKey="value"
															name="In"
															stroke={panel.color}
															dot={false}
															isAnimationActive={false}
														/>
														<Line
															dataKey="out"
															name="Out"
															stroke={panel.color}
															strokeDasharray="4 3"
															dot={false}
															isAnimationActive={false}
														/>
													</>
												) : (
													<Area
														type="monotone"
														dataKey="value"
														stroke={panel.color}
														fill={panel.color}
														fillOpacity={0.1}
														isAnimationActive={false}
													/>
												)}
											</ComposedChart>
										</ChartContainer>
										{marks.length > 0 && (
											<div
												role="group"
												aria-label={`Events on ${panel.title}`}
												className="pointer-events-none absolute"
												style={{ top: PLOT_TOP, bottom: X_AXIS_HEIGHT, left: AXIS_WIDTH, right: PLOT_RIGHT }}
											>
												{marks.map((event) => (
													<EventMark key={event.id} event={event} left={(event.ts - from) / (to - from)} />
												))}
											</div>
										)}
									</div>
								)}
							</section>
						);
					})}
				</div>
			)}
			{pod !== "" && moreEvents && (
				<p className="m-0 text-[12px] text-fg3">Older events in this window aren’t marked.</p>
			)}
		</section>
	);
}

function EventMark({ event, left }: { event: EventItem; left: number }) {
	const timePref = useTimePreferences();
	const warn = event.severity !== "normal";
	const at = formatClock(event.ts, false, timePref);
	return (
		<SpanTipTrigger
			entries={[{ title: event.title, meta: at }]}
			render={
				<button
					type="button"
					aria-label={`${event.title}, ${at}`}
					className="pointer-events-auto absolute inset-y-0 w-2 -translate-x-1/2 rounded-sm focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none"
					style={{ left: `${String(left * 100)}%` }}
				/>
			}
		>
			<i
				aria-hidden
				className={cn(
					"absolute inset-y-0 left-1/2 border-l-[1.5px] border-dashed",
					warn ? "border-warn-fg" : "border-fg3",
				)}
			/>
			<i
				aria-hidden
				className={cn(
					"absolute -top-[3px] left-1/2 size-[7px] -translate-x-1/2 rounded-full",
					warn ? "bg-warn-fg" : "bg-fg3",
				)}
			/>
		</SpanTipTrigger>
	);
}
