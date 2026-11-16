import { TimeAxis } from "./TimeAxis";
import type { SetTraceZoom } from "./useTraceZoom";
import { useMemo } from "react";
import type { ReactNode } from "react";
import type { TraceSpan } from "@gen/loco/observability/v1/observability_pb";

import { cn } from "@/lib/utils";

import { MIN_BLOCK, zoomLayout, lineage, zoomTo, type Zoom } from "../flame";

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
	markers,
	linkedSpan,
	onHoverSpan,
}: {
	trace: TraceSummary;
	selected: TraceSpan;
	onSelect: (span: TraceSpan) => void;
	label: (span: TraceSpan) => string;
	zoom: Zoom;
	setZoom: SetTraceZoom;
	highlight: string | null;
	markers: ReactNode;
	linkedSpan: string | null;
	onHoverSpan: (id: string | null) => void;
}) {
	const { color: resourceColor, layout, selfTime } = useTraceResources();
	const { blocks, depth } = useMemo(() => zoomLayout(layout, zoom), [layout, zoom]);
	const related = useMemo(() => lineage(trace, selected.spanId), [trace, selected.spanId]);
	const ticks = offsetTicks(trace.duration, zoom);
	return (
		<section aria-label="Flame graph" className="flex flex-col gap-1.5 rounded-card bg-panel px-3.5 pt-3 pb-3.5">
			<TimeAxis duration={trace.duration} zoom={zoom} onZoom={setZoom} highlight={highlight} />
			{markers}
			<div className="relative overflow-hidden" style={{ height: depth * ROW_PX }}>
				{ticks.map((t) => (
					<span
						key={String(t.nanos)}
						aria-hidden="true"
						className="tv-zoom-position absolute inset-y-0 border-l border-dashed border-line2"
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
							data-span-id={b.span.spanId}
							data-linked={linkedSpan === b.span.spanId || undefined}
							onFocus={() => {
								onHoverSpan(b.span.spanId);
							}}
							onBlur={() => {
								onHoverSpan(null);
							}}
							type="button"
							aria-pressed={on}
							aria-label={`${label(b.span)} ${b.span.name}, ${durationLabel(b.span.durationNanos)}${err ? ", error" : ""}`}
							title={`${b.span.name}\n${label(b.span)} · ${durationLabel(b.span.durationNanos)} · self ${durationLabel(selfTime(b.span))}\nDouble-click to zoom`}
							onClick={() => {
								onSelect(b.span);
							}}
							onDoubleClick={() => {
								setZoom(zoomTo(trace, b.span));
							}}
							className={cn(
								"tv-span-bar @container absolute h-6 min-w-0 overflow-hidden rounded-[3px] p-0 text-left text-foreground focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none",
								err && "text-bad-fg",
								!related.has(b.span.spanId) && "opacity-45",
								highlight !== null && traceResourceKey(b.span) !== highlight && "opacity-20",
								on && "shadow-[inset_0_0_0_1.5px_var(--fg)]",
								linkedSpan === b.span.spanId && "ring-2 ring-info",
							)}
							style={{
								left: `${String(Math.min(b.left, 1 - MIN_BLOCK) * PERCENT)}%`,
								width: `${String(Math.max(MIN_BLOCK, b.width) * PERCENT)}%`,
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
