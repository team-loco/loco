import { useMemo, useState } from "react";
import type { TraceSpan } from "@gen/loco/observability/v1/observability_pb";
import { useQueries } from "@tanstack/react-query";
import { useTransport } from "@connectrpc/connect-query";
import { Area, CartesianGrid, ComposedChart, Line, ReferenceArea, ReferenceLine, XAxis, YAxis } from "recharts";
import { ChartContainer, ChartTooltip } from "@/components/design/Chart";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { Button } from "@/components/design/Button";
import { SoonTag } from "@/components/design/SoonTag";
import { Spinner } from "@/components/design/Spinner";
import { useEnvironments } from "@/hooks/useEnvironment";
import { metricsQueryOptions } from "@/hooks/useQueryMetrics";
import { formatHourMinute, timeAxis } from "@/lib/time";
import { useTimePreferences } from "@/lib/use-time-preferences";
import { getErrorMessage } from "@/lib/error-handler";
import { formatPercent, percentAxis } from "@/lib/compute-chart";
import { trafficValue } from "@/lib/traffic";
import { cn } from "@/lib/utils";
import { percentPoints } from "@/pages/resource/metric";
import { formatDuration } from "@/pages/resource/format";
import { useObs } from "../context";
import type { TraceSummary } from "../traces";
import { traceBounds } from "./trace-time";
import { networkPoints, sampleAt, type InfraPoint } from "./infra-series";

const WINDOWS = [
	{ value: 5, label: "±5m" },
	{ value: 10, label: "±10m" },
	{ value: 30, label: "±30m" },
	{ value: 60, label: "±1h" },
];
const METRICS = [
	{ metric: "k8s.pod.cpu_limit_utilization", aggregation: "avg", direction: "" },
	{ metric: "k8s.pod.memory_limit_utilization", aggregation: "avg", direction: "" },
	{ metric: "k8s.pod.network.io", aggregation: "rate", direction: "receive" },
	{ metric: "k8s.pod.network.io", aggregation: "rate", direction: "transmit" },
];
const networkLabel = (point: InfraPoint | undefined) =>
	`${trafficValue(point?.value, "")} / ${trafficValue(point?.out, "")} B/s`;
const CHART_CONFIG = {
	value: { label: "Usage", color: "var(--series-0)" },
	out: { label: "Out", color: "var(--series-2)" },
};

export function Infrastructure({ trace, selected }: { trace: TraceSummary; selected: TraceSpan }) {
	const timePref = useTimePreferences();
	const { workspaceId } = useObs();
	const { active } = useEnvironments();
	const transport = useTransport();
	const [minutes, setMinutes] = useState(10);
	const [hoverTime, setHoverTime] = useState<number | null>(null);
	const pod = selected.resourceAttributes["k8s.pod.name"] ?? "";
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
				attributes: { "k8s.pod.name": pod, ...(metric.direction === "" ? {} : { direction: metric.direction }) },
				region: selected.resourceAttributes["loco.region"] ?? "",
				enabled: pod !== "" && selected.resourceId !== "",
			}),
		),
	});
	const cpu = queries[0]?.data,
		memory = queries[1]?.data,
		incoming = queries[2]?.data,
		outgoing = queries[3]?.data;
	const points = useMemo<InfraPoint[][]>(
		() => [
			percentPoints(cpu ?? [], selected.resourceId).map(({ time, pct }) => ({ time, value: pct })),
			percentPoints(memory ?? [], selected.resourceId).map(({ time, pct }) => ({ time, value: pct })),
			networkPoints(incoming, outgoing, selected.resourceId),
		],
		[cpu, memory, incoming, outgoing, selected.resourceId],
	);
	const started = Date.parse(selected.resourceAttributes["k8s.pod.start_time"] ?? "");
	const uptime = Number.isFinite(started) && started <= start ? formatDuration(start - started) : null;
	const panels = [
		{ title: "CPU", indices: [0], color: "var(--series-0)" },
		{ title: "Memory", indices: [1], color: "var(--series-1)" },
		{ title: "Network", indices: [2, 3], color: "var(--series-2)" },
	];
	return (
		<section aria-label="Trace infrastructure" className="flex min-w-0 flex-col gap-3 px-5 py-3">
			<div className="flex flex-wrap items-center gap-2">
				<ToggleGroup
					variant="segmented"
					aria-label="Infrastructure window"
					value={[String(minutes)]}
					onValueChange={(values) => {
						const next = WINDOWS.find((window) => String(window.value) === values[0]);
						if (next !== undefined) {
							setMinutes(next.value);
							setHoverTime(null);
						}
					}}
				>
					{WINDOWS.map((window) => (
						<ToggleGroupItem key={window.value} value={String(window.value)}>
							{window.label}
						</ToggleGroupItem>
					))}
				</ToggleGroup>
				<span className="ml-auto text-[12px] text-fg3">
					{uptime === null ? "Uptime not recorded" : `Up ${uptime} when this ran`}
				</span>
			</div>
			{pod === "" ? (
				<p className="m-0 text-[12px] text-fg3">No replica identity was recorded for this span.</p>
			) : (
				<div className="grid min-w-0 gap-5 min-[1180px]:grid-cols-[minmax(0,1fr)_200px]">
					<div className="grid min-w-0 gap-4 sm:grid-cols-3">
						{panels.map((panel, index) => {
							const data = points[index] ?? [];
							const network = panel.title === "Network";
							const fmt = (value: number | undefined) =>
								network ? trafficValue(value, "B/s") : value === undefined ? "—" : formatPercent(value);
							const sample = sampleAt(data, hoverTime ?? start);
							const peak = data.reduce<number | undefined>(
								(max, point) =>
									point.value === undefined ? max : max === undefined ? point.value : Math.max(max, point.value),
								undefined,
							);
							const hot = !network && peak !== undefined && peak > 85;
							const error = panel.indices.map((i) => queries[i]?.error).find((e) => e !== null && e !== undefined);
							const loading = panel.indices.some((i) => queries[i]?.isLoading === true);
							return (
								<section key={panel.title} aria-label={`${panel.title} for ${pod}`} className="min-w-0">
									<div className="flex min-h-5 items-baseline justify-between gap-2">
										<h4 className="m-0 text-[12px] font-medium text-fg2">{panel.title}</h4>
										<output
											aria-live="off"
											aria-label={`${panel.title} at ${hoverTime === null ? "trace start" : "cursor"}`}
											className="min-w-0 truncate font-mono text-[12px]"
										>
											{network ? networkLabel(sample) : fmt(sample?.value)}
											{hoverTime !== null && ` at ${formatHourMinute(hoverTime, timePref)}`}
										</output>
									</div>
									<div className={cn("h-4 text-[11px] text-fg3", hot && "text-bad-fg")}>
										{network ? (
											<span className="inline-flex items-center gap-1">
												<i aria-hidden className="w-2 border-t-2" style={{ borderColor: panel.color }} />
												In{" "}
												<i
													aria-hidden
													className="ml-2 w-2 border-t-2 border-dashed"
													style={{ borderColor: panel.color }}
												/>
												Out
											</span>
										) : (
											`Peak ${fmt(peak)}${hot ? " · High" : ""}`
										)}
									</div>
									{loading ? (
										<div role="status" className="flex h-36 items-center justify-center gap-2 text-fg3">
											<Spinner />
											Loading metrics…
										</div>
									) : error !== undefined ? (
										<p role="alert" className="m-0 flex h-36 items-center text-[12px] text-bad-fg">
											{getErrorMessage(error, "Couldn’t load replica metrics")}
										</p>
									) : data.length === 0 ? (
										<div className="flex h-36 items-center justify-center rounded-md border border-dashed border-line text-[12px] text-fg3">
											No metrics in this range
										</div>
									) : (
										<ChartContainer
											role="group"
											aria-label={`${panel.title} usage`}
											config={CHART_CONFIG}
											className="tv-infra-chart h-36 w-full aspect-auto"
										>
											<ComposedChart
												data={data}
												margin={{ top: 8, right: 8, bottom: 0, left: 0 }}
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
												/>
												<YAxis
													domain={network ? [0, "auto"] : [0, percentAxis(peak ?? 0).maximum]}
													tickFormatter={fmt}
													tickLine={false}
													axisLine={false}
													width={network ? 64 : 40}
												/>
												<ReferenceArea x1={start} x2={end} fill="var(--fg3)" fillOpacity={0.18} />
												{!network && (
													<ReferenceLine
														y={100}
														stroke="var(--err)"
														label={{ value: "Limit 100%", fill: "var(--err)", fontSize: 9, position: "insideTopLeft" }}
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
									)}
								</section>
							);
						})}
					</div>
					<section
						aria-label={`Events on ${pod}`}
						className="min-w-0 border-line min-[1180px]:border-l min-[1180px]:pl-5"
					>
						<h4 className="m-0 mb-2 break-all text-[12px] font-medium text-fg2">Events on {pod}</h4>
						<Button variant="ghost" size="sm" disabled>
							Replica events <SoonTag />
						</Button>
					</section>
				</div>
			)}
		</section>
	);
}
