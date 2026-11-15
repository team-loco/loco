import type { TraceSpan } from "@gen/loco/observability/v1/observability_pb";

import { cn } from "@/lib/utils";

import { flameLayout, lineage, selfTime, zoomTo, type Zoom } from "../flame";

import { traceResourceKey } from "../trace-service";
import { useTraceResources } from "./TraceColors";
import { offsetTicks } from "./plot";
import { durationLabel, spanError, type TraceSummary } from "../traces";
const ROW_PX = 24;
const PERCENT = 100;

export function FlameGraph({
	trace,
	selected,
	onSelect,
	label,
	zoom,
	setZoom,
	highlight,
}: {
	trace: TraceSummary;
	selected: TraceSpan;
	onSelect: (span: TraceSpan) => void;
	label: (span: TraceSpan) => string;
	zoom: Zoom;
	setZoom: (zoom: Zoom) => void;
	highlight: string | null;
}) {
	const { color: resourceColor } = useTraceResources();
	const { blocks, depth } = flameLayout(trace, zoom);
	const related = lineage(trace, selected.spanId);
	const ticks = offsetTicks(trace.duration, zoom);
	return (
		<section aria-label="Flame graph" className="flex flex-col gap-1.5 rounded-card bg-panel px-3.5 pt-3 pb-3.5">
			<div className="relative h-3.5 font-mono text-[11px] text-fg3" aria-hidden="true">
				{ticks.map((t, i) => (
					<span
						key={String(t.nanos)}
						className={cn(
							"absolute whitespace-nowrap",
							i > 0 && i < ticks.length - 1 && "hidden sm:inline",
							i === 0 ? "" : i === ticks.length - 1 ? "-translate-x-full" : "-translate-x-1/2",
						)}
						style={{ left: `${String(t.left * PERCENT)}%` }}
					>
						{durationLabel(t.nanos)}
					</span>
				))}
			</div>
			<div className="relative overflow-hidden" style={{ height: depth * ROW_PX }}>
				{ticks.map((t) => (
					<span
						key={String(t.nanos)}
						aria-hidden="true"
						className="absolute inset-y-0 border-l border-dashed border-line2"
						style={{ left: `${String(t.left * PERCENT)}%` }}
					/>
				))}
				{blocks.map((b) => {
					const err = spanError(b.span);
					const on = b.span.spanId === selected.spanId;
					const color = err ? "var(--err)" : resourceColor(b.span);
					return (
						<button
							key={b.span.spanId}
							type="button"
							aria-pressed={on}
							aria-label={`${label(b.span)} ${b.span.name}, ${durationLabel(b.span.durationNanos)}${err ? ", error" : ""}`}
							title={`${b.span.name}\n${label(b.span)} · ${durationLabel(b.span.durationNanos)} · self ${durationLabel(selfTime(trace, b.span))}\nDouble-click to zoom`}
							onClick={() => {
								onSelect(b.span);
							}}
							onDoubleClick={() => {
								setZoom(zoomTo(trace, b.span));
							}}
							className={cn(
								"@container absolute h-6 min-w-0 overflow-hidden rounded-[3px] p-0 text-left text-foreground focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none",
								err && "text-bad-fg",
								!related.has(b.span.spanId) && "opacity-45",
								highlight !== null && traceResourceKey(b.span) !== highlight && "opacity-20",
								on && "shadow-[inset_0_0_0_1.5px_var(--fg)]",
							)}
							style={{
								left: `${String(b.left * PERCENT)}%`,
								width: `${String(b.width * PERCENT)}%`,
								top: b.depth * ROW_PX,
								background: `color-mix(in oklab, ${color} ${err ? "26%" : "34%"}, var(--bg))`,
							}}
						>
							<span className="flex h-full w-full min-w-0 items-center gap-2 overflow-hidden px-1.5">
								<span className="min-w-0 flex-1 truncate font-mono text-[11.5px]">{b.span.name}</span>
								<span className="hidden shrink-0 font-mono text-[11px] whitespace-nowrap opacity-70 @[160px]:block">
									{durationLabel(b.span.durationNanos)}
								</span>
							</span>
						</button>
					);
				})}
			</div>
		</section>
	);
}
