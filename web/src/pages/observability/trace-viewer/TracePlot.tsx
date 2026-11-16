import type { SetTraceZoom } from "./useTraceZoom";
import { useRef, useState } from "react";
import type { TraceSpan } from "@gen/loco/observability/v1/observability_pb";
import { ZoomOutIcon } from "lucide-react";
import { Button } from "@/components/design/Button";
import { ToggleGroup, ToggleGroupItem } from "@/components/design/ToggleGroup";
import { FULL_ZOOM, type Zoom } from "../flame";
import { durationLabel, type TraceSummary } from "../traces";
import { FlameGraph } from "./FlameGraph";
import { Waterfall } from "./Waterfall";
import { SpanList } from "./SpanList";
import { boundedZoom, zoomAround } from "./plot";

import type { LogRow } from "../rows";
import { LogMarkers } from "./LogMarkers";

type View = "flame" | "waterfall" | "list";
const laneBounds = (element: HTMLElement) =>
	(element.querySelector<HTMLElement>("[data-trace-lane]") ?? element).getBoundingClientRect();
export function TracePlot({
	trace,
	selected,
	onSelect,
	label,
	zoom,
	onZoom,
	highlight,
	logs,
	highlightedLogs,
	linkedSpan,
	onHoverSpan,
	onHoverTime,
	onHoverLog,
	onOpenLog,
}: {
	trace: TraceSummary;
	selected: TraceSpan;
	onSelect: (span: TraceSpan) => void;
	label: (span: TraceSpan) => string;
	zoom: Zoom;
	onZoom: SetTraceZoom;
	highlight: string | null;
	logs: LogRow[];
	highlightedLogs: Set<string>;
	linkedSpan: string | null;
	onHoverSpan: (id: string | null) => void;
	onHoverTime: (fraction: number | null) => void;
	onHoverLog: (row: LogRow | null) => void;
	onOpenLog: (row: LogRow) => void;
}) {
	const [view, setView] = useState<View>("flame");
	const cursorRef = useRef<HTMLDivElement>(null);
	const dragRef = useRef<{ x: number; zoom: Zoom } | null>(null);
	const suppressClickRefRef = useRef(false);
	const markers = { trace, zoom, logs, highlighted: highlightedLogs, onHover: onHoverLog, onOpen: onOpenLog };
	const width = zoom.b - zoom.a;
	const offset = (f: number) => durationLabel(BigInt(Math.round(Number(trace.duration) * f)));
	return (
		<div className="flex min-w-0 flex-col gap-2.5">
			<div className="flex flex-wrap items-center gap-x-3 gap-y-2">
				<ToggleGroup
					variant="segmented"
					aria-label="Trace view"
					className="tv-view-tabs"
					value={[view]}
					onValueChange={(values) => {
						const next = values[0];
						if (next === "flame" || next === "waterfall" || next === "list") setView(next);
					}}
				>
					<ToggleGroupItem value="flame">Flame graph</ToggleGroupItem>
					<ToggleGroupItem value="waterfall">Waterfall</ToggleGroupItem>
					<ToggleGroupItem value="list">Span list</ToggleGroupItem>
				</ToggleGroup>
				<span className="text-[12px] text-fg3">Drag the time axis or double-click a span to zoom</span>
				{width < 1 && (
					<>
						<span className="ml-auto font-mono text-[11px] text-fg3">
							{offset(zoom.a)}–{offset(zoom.b)}
						</span>
						<Button
							variant="outline"
							size="sm"
							onClick={() => {
								onZoom(FULL_ZOOM);
							}}
						>
							<ZoomOutIcon />
							Reset zoom
						</Button>
					</>
				)}
			</div>
			{view === "list" ? (
				<SpanList trace={trace} />
			) : (
				<div
					className="relative min-w-0"
					data-trace-plot={view}
					ref={(element) => {
						if (element === null) return;
						const wheel = (e: WheelEvent) => {
							if (!e.ctrlKey && !e.metaKey && !e.shiftKey && Math.abs(e.deltaX) < Math.abs(e.deltaY)) return;
							e.preventDefault();
							const rect = laneBounds(element);
							if (e.shiftKey || Math.abs(e.deltaX) > Math.abs(e.deltaY))
								onZoom((z) => boundedZoom(z.a + ((e.deltaX || e.deltaY) / rect.width) * (z.b - z.a), z.b - z.a), true);
							else onZoom((z) => zoomAround(z, (e.clientX - rect.left) / rect.width, e.deltaY), true);
						};
						element.addEventListener("wheel", wheel, { passive: false });
						return () => {
							element.removeEventListener("wheel", wheel);
						};
					}}
					onPointerDown={(e) => {
						if (width >= 1 || e.button !== 0 || (e.target as HTMLElement).closest('[role="separator"], [data-trace-axis], [role="slider"]') !== null)
							return;
						suppressClickRefRef.current = false;
						dragRef.current = { x: e.clientX, zoom };
					}}
					onPointerMove={(e) => {
						const rect = laneBounds(e.currentTarget);
						const fraction = (e.clientX - rect.left) / rect.width;

						const cursor = cursorRef.current;
						if (cursor !== null) {
							cursor.hidden = fraction < 0 || fraction > 1;
							cursor.style.left = `${String(e.clientX - e.currentTarget.getBoundingClientRect().left)}px`;
							const readout = cursor.firstElementChild as HTMLSpanElement;
							readout.textContent = offset(zoom.a + fraction * width);
							readout.style.left = fraction > 0.8 ? "auto" : "4px";
							readout.style.right = fraction > 0.8 ? "4px" : "auto";
						}

						const spanId = (e.target as HTMLElement).closest<HTMLElement>("[data-span-id]")?.dataset["spanId"];
						if (spanId !== undefined) onHoverSpan(spanId);
						else onHoverTime(fraction < 0 || fraction > 1 ? null : zoom.a + fraction * width);
						const drag = dragRef.current;
						if (drag !== null && e.buttons === 1) {
							const delta = e.clientX - drag.x;
							if (Math.abs(delta) > 3) {
								suppressClickRefRef.current = true;
								e.currentTarget.setPointerCapture(e.pointerId);
								onZoom(
									boundedZoom(
										drag.zoom.a - (delta / rect.width) * (drag.zoom.b - drag.zoom.a),
										drag.zoom.b - drag.zoom.a,
									),
									true,
								);
							}
						}
					}}
					onPointerUp={() => {
						dragRef.current = null;
					}}
					onLostPointerCapture={() => {
						dragRef.current = null;
					}}
					onPointerCancel={() => {
						dragRef.current = null;
						suppressClickRefRef.current = false;
					}}
					onClickCapture={(e) => {
						if (suppressClickRefRef.current) {
							e.preventDefault();
							e.stopPropagation();
						}
						suppressClickRefRef.current = false;
					}}
					onPointerLeave={() => {
						if (cursorRef.current !== null) cursorRef.current.hidden = true;

						onHoverSpan(null);
						onHoverTime(null);
					}}
				>
					{view === "flame" ? (
						<FlameGraph
							trace={trace}
							selected={selected}
							onSelect={onSelect}
							label={label}
							zoom={zoom}
							setZoom={onZoom}
							highlight={highlight}
							markers={<LogMarkers {...markers} />}
							linkedSpan={linkedSpan}
							onHoverSpan={onHoverSpan}
						/>
					) : (
						<Waterfall
							trace={trace}
							selected={selected}
							onSelect={onSelect}
							label={label}
							zoom={zoom}
							onZoom={onZoom}
							highlight={highlight}
							markers={<LogMarkers {...markers} />}
							linkedSpan={linkedSpan}
							onHoverSpan={onHoverSpan}
						/>
					)}

					<div
						ref={cursorRef}
						hidden
						aria-hidden
						className="pointer-events-none absolute inset-y-0 z-10 border-l border-fg3"
					>
						<span className="absolute top-0 rounded-sm border border-line bg-background px-1 py-0.5 font-mono text-[10px]" />
					</div>
				</div>
			)}
		</div>
	);
}
