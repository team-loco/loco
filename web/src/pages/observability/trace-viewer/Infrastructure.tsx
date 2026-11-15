import { useTimePreferences } from "@/lib/use-time-preferences";
import { useState } from "react";
import type { TraceSpan } from "@gen/loco/observability/v1/observability_pb";
import { useQueries } from "@tanstack/react-query";
import { useTransport } from "@connectrpc/connect-query";
import { Area, CartesianGrid, ComposedChart, ReferenceArea, ReferenceLine, XAxis, YAxis } from "recharts";
import { ChartContainer, ChartTooltip, ChartTooltipContent } from "@/components/design/Chart";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuTrigger,
} from "@/components/design/DropdownMenu";
import { Button } from "@/components/design/Button";
import { SoonTag } from "@/components/design/SoonTag";
import { Spinner } from "@/components/design/Spinner";
import { useEnvironments } from "@/hooks/useEnvironment";
import { metricsQueryOptions } from "@/hooks/useQueryMetrics";
import { getErrorMessage } from "@/lib/error-handler";
import { formatHourMinute, timeAxis, tsMs } from "@/lib/time";
import { useObs } from "../context";
import type { TraceSummary } from "../traces";
import { formatDuration } from "@/pages/resource/format";
import { traceServiceLabel } from "../trace-service";

const WINDOW_MS = 10 * 60_000;
const METRICS = [
	{ title: "CPU", metric: "k8s.pod.cpu_limit_utilization" },
	{ title: "Memory", metric: "k8s.pod.memory_limit_utilization" },
];
const CHART_CONFIG = { value: { label: "Usage", color: "var(--series-0)" } };

export function Infrastructure({ trace, selected }: { trace: TraceSummary; selected: TraceSpan }) {
	const timePref = useTimePreferences();
	const { workspaceId, resourceById } = useObs();
	const { active } = useEnvironments();
	const transport = useTransport();
	const replicas = [
		...new Map(
			trace.spans
				.filter((s) => s.resourceAttributes["k8s.pod.name"])
				.map((s) => [`${s.resourceId}:${s.resourceAttributes["k8s.pod.name"] ?? ""}`, s]),
		).values(),
	];
	const [picked, setPicked] = useState<string | null>(null);
	const span = replicas.find((s) => s.spanId === picked) ?? selected;
	const pod = span.resourceAttributes["k8s.pod.name"] ?? "";
	const resource = resourceById.get(span.resourceId);
	const spec = resource?.raw.spec?.spec;
	const service =
		spec?.case === "service" ? spec.value.regions[span.resourceAttributes["loco.region"] ?? ""] : undefined;
	const start = Number(trace.start / 1_000_000n);
	const end = start + Number(trace.duration) / 1_000_000;
	const from = start - WINDOW_MS;
	const to = end + WINDOW_MS;
	const queries = useQueries({
		queries: METRICS.map((m) =>
			metricsQueryOptions(transport, {
				workspaceId,
				environmentId: active?.id ?? "",
				resourceIds: [span.resourceId],
				spanMs: to - from,
				endMs: to,
				metricName: m.metric,
				attributes: { "k8s.pod.name": pod },
				region: span.resourceAttributes["loco.region"] ?? "",
				enabled: pod !== "" && span.resourceId !== "",
			}),
		),
	});
	const podStart = span.resourceAttributes["k8s.pod.start_time"];
	const started = podStart === undefined ? NaN : Date.parse(podStart);
	const facts = [
		["Replica", pod],
		["Node", span.resourceAttributes["k8s.node.name"] ?? "Not recorded"],
		[
			"Uptime",
			Number.isFinite(started) && started <= start
				? `Up for ${formatDuration(start - started)} when this ran`
				: "Not recorded",
		],
	];
	return (
		<section aria-label="Trace infrastructure" className="flex min-w-0 flex-col gap-4 px-5 py-4">
			{(replicas.length > 1 || (replicas.length > 0 && pod === "")) && (
				<DropdownMenu>
					<DropdownMenuTrigger
						render={
							<Button
								variant="outline"
								size="sm"
								aria-label="Choose replica"
								className="max-w-full self-start truncate"
							/>
						}
					>
						{pod || "Choose replica"}
					</DropdownMenuTrigger>
					<DropdownMenuContent>
						{replicas.map((s) => (
							<DropdownMenuItem
								key={s.spanId}
								onClick={() => {
									setPicked(s.spanId);
								}}
							>
								{traceServiceLabel(s, resourceById.get(s.resourceId)?.name)} · {s.resourceAttributes["k8s.pod.name"]}
							</DropdownMenuItem>
						))}
					</DropdownMenuContent>
				</DropdownMenu>
			)}
			{pod === "" ? (
				<p className="m-0 text-[12px] text-fg3">No replica identity was recorded for this span.</p>
			) : (
				<>
					<dl className="m-0 grid grid-cols-1 gap-x-6 gap-y-2 text-[12px] sm:grid-cols-3">
						{facts.map(([key, value]) => (
							<div key={key}>
								<dt className="text-fg3">{key}</dt>
								<dd className="m-0 break-all font-mono">{value}</dd>
							</div>
						))}
					</dl>
					<div className="grid min-w-0 gap-5 sm:grid-cols-2">
						{METRICS.map((m, i) => {
							const query = queries[i];
							const points = (query?.data ?? [])
								.flatMap((s) => s.points.map((p) => ({ time: tsMs(p.timestamp), value: p.value * 100 })))
								.toSorted((a, b) => a.time - b.time);
							const limit = i === 0 ? service?.cpu : service?.memory;
							return (
								<div key={m.title} className="min-w-0">
									<div className="mb-2 flex items-center gap-2 text-[12px]">
										<span>{m.title}</span>
										<span className="ml-auto text-fg3">
											% of limit{limit !== undefined && limit !== "" ? ` · configured ${limit}` : ""}
										</span>
									</div>
									{query?.isLoading === true ? (
										<div role="status" className="flex h-32 items-center justify-center gap-2 text-fg3">
											<Spinner />
											Loading metrics…
										</div>
									) : query?.error !== null && query?.error !== undefined ? (
										<p role="alert" className="text-bad-fg">
											{getErrorMessage(query.error, "Couldn’t load replica metrics")}
										</p>
									) : points.length === 0 ? (
										<div className="flex h-32 items-center justify-center rounded-md border border-dashed border-line text-[12px] text-fg3">
											No metrics in this range
										</div>
									) : (
										<ChartContainer
											config={CHART_CONFIG}
											className="tv-infra-chart h-36 w-full"
											aria-label={`${m.title} for ${pod}, ten minutes around trace`}
										>
											<ComposedChart data={points} margin={{ top: 8, right: 12, bottom: 0, left: 0 }}>
												<CartesianGrid vertical={false} stroke="var(--line)" />
												<XAxis
													dataKey="time"
													type="number"
													domain={[from, to]}
													ticks={timeAxis(from, to, timePref)}
													tickFormatter={(ms: number) => formatHourMinute(ms, timePref)}
													tickLine={false}
													axisLine={false}
													minTickGap={30}
												/>
												<YAxis
													domain={[0, "auto"]}
													tickFormatter={(n: number) => `${String(n)}%`}
													tickLine={false}
													axisLine={false}
													width={44}
												/>
												<ReferenceArea x1={start} x2={end} fill="var(--fg3)" fillOpacity={0.18} />
												<ReferenceLine
													ifOverflow="extendDomain"
													y={100}
													stroke="var(--fg3)"
													strokeDasharray="4 4"
													label={{ value: "limit", fill: "var(--fg3)", fontSize: 10 }}
												/>
												<ChartTooltip
													content={
														<ChartTooltipContent
															labelFormatter={(label) => formatHourMinute(Number(label), timePref)}
															formatter={(value) => `${Number(value).toFixed(1)}%`}
														/>
													}
												/>
												<Area
													type="monotone"
													dataKey="value"
													stroke="var(--series-0)"
													fill="var(--series-0)"
													fillOpacity={0.12}
													isAnimationActive={false}
												/>
											</ComposedChart>
										</ChartContainer>
									)}
								</div>
							);
						})}
					</div>
					<div className="flex items-center gap-2 text-[12px] text-fg3">
						<Button variant="ghost" size="sm" disabled>
							Replica events, ±10 min <SoonTag />
						</Button>
					</div>
				</>
			)}
		</section>
	);
}
