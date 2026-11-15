import type { TraceSpan } from "@gen/loco/observability/v1/observability_pb";
import { ZoomOutIcon } from "lucide-react";

import { Button } from "@/components/design/Button";

import { cn } from "@/lib/utils";

import { flameLayout, FULL_ZOOM, lineage, selfTime, zoomTo, type Zoom } from "../flame";

import { traceResourceKey } from "../trace-service";
import { useTraceResources } from "./TraceColors";
import { axisLabel, durationLabel, spanError, type TraceSummary } from "../traces";

const ROW_PX = 24;
const TICKS = [0, 0.2, 0.4, 0.6, 0.8, 1];
const PERCENT = 100;
const TICK_SCALE = 1_000_000;

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
	const width = zoom.b - zoom.a;
	const zoomed = width < 1;
	return (
		<section aria-label="Flame graph" className="flex flex-col gap-1.5 rounded-card bg-panel px-3.5 pt-3 pb-3.5">
			<div className="flex min-h-7 flex-wrap items-center gap-x-4 gap-y-1.5">
				{zoomed && (
					<>
						<span className="font-mono text-[12px] text-fg3">{(1 / width).toFixed(1)}×</span>
						<Button
							variant="outline"
							size="sm"
							onClick={() => {
								setZoom(FULL_ZOOM);
							}}
						>
							<ZoomOutIcon />
							Reset zoom
						</Button>
					</>
				)}
			</div>
			<div className="relative h-3.5 font-mono text-[11px] text-fg3" aria-hidden="true">
				{TICKS.map((f, i) => (
					<span
						key={f}
						className={cn(
							"absolute whitespace-nowrap",
							i === 0 ? "" : i === TICKS.length - 1 ? "-translate-x-full" : "-translate-x-1/2",
						)}
						style={{ left: `${String(f * PERCENT)}%` }}
					>
						{axisLabel(
							(trace.duration * BigInt(Math.round((zoom.a + f * width) * TICK_SCALE))) / BigInt(TICK_SCALE),
							trace.duration,
						)}
					</span>
				))}
			</div>
			<div className="relative overflow-hidden" style={{ height: depth * ROW_PX }}>
				{TICKS.map((f) => (
					<span
						key={f}
						aria-hidden="true"
						className="absolute inset-y-0 border-l border-dashed border-line2"
						style={{ left: `${String(f * PERCENT)}%` }}
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
								"@container absolute h-[22px] min-w-0 overflow-hidden rounded-[3px] p-0 text-left text-foreground focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none",
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
			<span className="text-[11.5px] text-fg3">Select a span for details. Double-click a span to zoom into it.</span>
		</section>
	);
}
