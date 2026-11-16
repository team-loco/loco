import { useState } from "react";
import type { TraceSpan } from "@gen/loco/observability/v1/observability_pb";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { Button } from "@/components/design/Button";
import { SoonTag } from "@/components/design/SoonTag";
import { formatHourMinute } from "@/lib/time";
import { useTimePreferences } from "@/lib/use-time-preferences";
import { formatCpu, formatMemory, parseCpuMilli, parseMemMi } from "@/lib/quantity";
import { formatDuration } from "@/pages/resource/format";
import { useObs } from "../context";
import { spanStart, type TraceSummary } from "../traces";
import { traceBounds } from "./trace-time";

const WINDOWS = [
	{ value: 5, label: "±5m" },
	{ value: 10, label: "±10m" },
	{ value: 30, label: "±30m" },
	{ value: 60, label: "±1h" },
];

export function Infrastructure({ trace, selected }: { trace: TraceSummary; selected: TraceSpan }) {
	const timePref = useTimePreferences();
	const { resourceById } = useObs();
	const [minutes, setMinutes] = useState(10);
	const pod = selected.resourceAttributes["k8s.pod.name"] ?? "";
	const spec = resourceById.get(selected.resourceId)?.raw.spec?.spec;
	const service =
		spec?.case === "service" ? spec.value.regions[selected.resourceAttributes["loco.region"] ?? ""] : undefined;
	const { start, end } = traceBounds(trace);
	const from = start - minutes * 60_000;
	const to = end + minutes * 60_000;
	const started = Date.parse(selected.resourceAttributes["k8s.pod.start_time"] ?? "");
	const ranAt = Number(spanStart(selected) / 1_000_000n);
	const uptime = Number.isFinite(started) && started <= ranAt ? formatDuration(ranAt - started) : null;
	const sizing = [
		{ title: "CPU", value: service?.cpu ? formatCpu(parseCpuMilli(service.cpu)) : null },
		{ title: "Memory", value: service?.memory ? formatMemory(parseMemMi(service.memory)) : null },
		{ title: "Network", value: null },
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
						if (next !== undefined) setMinutes(next.value);
					}}
				>
					{WINDOWS.map((window) => (
						<ToggleGroupItem key={window.value} value={String(window.value)}>
							{window.label}
						</ToggleGroupItem>
					))}
				</ToggleGroup>
				<span className="ml-auto text-[12px] text-fg3">
					{uptime === null ? (
						<>
							Uptime <SoonTag />
						</>
					) : (
						<>Up {uptime} when this ran</>
					)}
				</span>
			</div>
			{pod === "" ? (
				<p className="m-0 text-[12px] text-fg3">No replica identity was recorded for this span.</p>
			) : (
				<div className="grid min-w-0 gap-5 min-[1180px]:grid-cols-[minmax(0,1fr)_200px]">
					<div className="grid min-w-0 gap-4 sm:grid-cols-3">
						{sizing.map((metric) => (
							<section key={metric.title} aria-label={`${metric.title} for ${pod}`} className="min-w-0">
								<div className="mb-2 flex items-center justify-between gap-2 text-[12px]">
									<h4 className="m-0 font-medium text-fg2">{metric.title}</h4>
								</div>
								<p className="m-0 mb-2 min-h-8 text-[11px] text-fg3">
									{metric.title === "Network" ? (
										"Receive / transmit"
									) : metric.value === null ? (
										<>
											Configured sizing <SoonTag />
										</>
									) : (
										<>
											Configured limit / request <span className="font-mono text-fg2">{metric.value}</span>
										</>
									)}
								</p>
								<div
									aria-disabled="true"
									className="flex h-[120px] gap-1 items-center justify-center rounded-md border border-dashed border-line bg-panel text-[12px] text-fg3"
								>
									Replica {metric.title.toLowerCase()} <SoonTag />
								</div>
								<div
									className="mt-1 flex justify-between gap-1 font-mono text-[10px] text-fg3"
									role="group"
									aria-label="Infrastructure time range"
								>
									<span>{formatHourMinute(from, timePref)}</span>
									<span>{formatHourMinute(to, timePref)}</span>
								</div>
							</section>
						))}
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
