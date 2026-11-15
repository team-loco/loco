import { useMemo, useRef, useState } from "react";
import type { ReactNode } from "react";
import type { TraceSpan } from "@gen/loco/observability/v1/observability_pb";
import { ChevronDownIcon, ChevronRightIcon } from "lucide-react";
import { cn } from "@/lib/utils";
import { zoomTo, type Zoom } from "../flame";
import { durationLabel, spanStart, spanError, type TraceSummary } from "../traces";
import { traceResourceKey } from "../trace-service";
import { useTraceResources } from "./TraceColors";
import { offsetTicks } from "./plot";

export function Waterfall({
	trace,
	selected,
	zoom,
	onZoom,
	onSelect,
	highlight,
	label,
	markers,
	linkedSpan,
	onHoverSpan,
}: {
	trace: TraceSummary;
	selected: TraceSpan;
	zoom: Zoom;
	onZoom: (zoom: Zoom) => void;
	onSelect: (span: TraceSpan) => void;
	highlight: string | null;
	markers: ReactNode;
	linkedSpan: string | null;
	onHoverSpan: (id: string | null) => void;
	label: (span: TraceSpan) => string;
}) {
	const { color, tree: all, children: childrenById } = useTraceResources();
	const [collapsed, setCollapsed] = useState<Set<string>>(() => new Set());
	const [nameWidth, setNameWidth] = useState(240);
	const resizeRef = useRef<{ x: number; width: number } | null>(null);

	const rows = useMemo(() => {
		const hidden = new Set<string>();
		return all.filter((row) => {
			if (hidden.has(row.span.parentSpanId)) hidden.add(row.span.spanId);
			const visible = !hidden.has(row.span.spanId);
			if (collapsed.has(row.span.spanId)) hidden.add(row.span.spanId);
			return visible;
		});
	}, [all, collapsed]);

	const toggle = (id: string) => {
		setCollapsed((prev) => {
			const next = new Set(prev);
			if (next.has(id)) next.delete(id);
			else next.add(id);
			return next;
		});
	};
	const ticks = offsetTicks(trace.duration, zoom);
	const width = zoom.b - zoom.a;
	const clamp = (value: number) => Math.max(120, Math.min(480, value));
	return (
		<section aria-label="Waterfall" className="tv-waterfall min-w-0 overflow-auto rounded-md border border-line">
			<div className="relative flex border-b border-line bg-panel py-2 font-mono text-[11px] text-fg3">
				<span className="shrink-0 px-2" style={{ width: nameWidth }}>
					Span
				</span>
				<div
					role="separator"
					aria-label="Resize span column"
					aria-orientation="vertical"
					aria-valuenow={nameWidth}
					aria-valuemin={120}
					aria-valuemax={480}
					tabIndex={0}
					className="absolute inset-y-0 z-10 w-2 -translate-x-1/2 cursor-col-resize touch-none border-x border-line focus-visible:ring-2 focus-visible:ring-ring"
					style={{ left: nameWidth }}
					onPointerDown={(e) => {
						e.currentTarget.setPointerCapture(e.pointerId);
						resizeRef.current = { x: e.clientX, width: nameWidth };
					}}
					onPointerMove={(e) => {
						if (resizeRef.current !== null)
							setNameWidth(clamp(resizeRef.current.width + e.clientX - resizeRef.current.x));
					}}
					onLostPointerCapture={() => {
						resizeRef.current = null;
					}}
					onKeyDown={(e) => {
						if (e.key === "ArrowLeft" || e.key === "ArrowRight") {
							e.preventDefault();
							setNameWidth(clamp(nameWidth + (e.key === "ArrowLeft" ? -16 : 16)));
						}
					}}
				/>
				<div data-trace-lane className="relative min-w-[100px] flex-1">
					<div className="relative h-4">
						{" "}
						{ticks.map((t, index) => (
							<span
								key={String(t.nanos)}
								className={cn(
									"absolute whitespace-nowrap",
									index > 0 && index < ticks.length - 1 && "hidden sm:block",
									index === 0 ? "" : index === ticks.length - 1 ? "-translate-x-full" : "-translate-x-1/2",
								)}
								style={{ left: `${String(t.left * 100)}%` }}
							>
								{durationLabel(t.nanos)}
							</span>
						))}
					</div>
					{markers}
				</div>
			</div>
			{rows.map((row, index) => {
				const span = row.span;
				const children = childrenById.get(span.spanId)?.length ?? 0;
				const start = Number(spanStart(span) - trace.start) / Number(trace.duration || 1n);
				const end = start + Number(span.durationNanos) / Number(trace.duration || 1n);
				const left = Math.max(0, (start - zoom.a) / width);
				const right = Math.min(1, (end - zoom.a) / width);
				const off = end < zoom.a || start > zoom.b;
				return (
					<div
						key={span.spanId}
						data-span-id={span.spanId}
						className={cn(
							"tv-waterfall-row flex min-h-8 items-stretch border-b border-line last:border-0",
							selected.spanId === span.spanId && "bg-panel2",
							highlight !== null && traceResourceKey(span) !== highlight && "opacity-30",
							linkedSpan === span.spanId && "ring-2 ring-inset ring-info",
						)}
					>
						<div
							className="flex shrink-0 items-center overflow-hidden pr-2"
							style={{ width: nameWidth, paddingLeft: 8 + row.depth * 12 }}
						>
							<button
								type="button"
								aria-label={`${collapsed.has(span.spanId) ? "Expand" : "Collapse"} ${span.name}`}
								aria-expanded={children === 0 ? undefined : !collapsed.has(span.spanId)}
								disabled={children === 0}
								onClick={() => {
									toggle(span.spanId);
								}}
								className="flex size-5 shrink-0 items-center justify-center text-fg3 hover:bg-panel2 disabled:invisible"
							>
								{collapsed.has(span.spanId) ? (
									<ChevronRightIcon className="size-3" />
								) : (
									<ChevronDownIcon className="size-3" />
								)}
							</button>
							<i className="mr-1.5 size-2 shrink-0 rounded-full" style={{ background: color(span) }} />
							<button
								type="button"
								aria-pressed={selected.spanId === span.spanId}
								title={`${span.name} · ${label(span)}`}
								onClick={() => {
									onSelect(span);
								}}
								onKeyDown={(e) => {
									if (e.key === "ArrowDown" || e.key === "ArrowUp") {
										e.preventDefault();
										const next = rows[index + (e.key === "ArrowDown" ? 1 : -1)];
										if (next !== undefined) {
											onSelect(next.span);
											e.currentTarget
												.closest("section")
												?.querySelector<HTMLButtonElement>(`[data-span="${next.span.spanId}"]`)
												?.focus();
										}
									} else if (e.key === "ArrowLeft" && children > 0 && !collapsed.has(span.spanId)) {
										e.preventDefault();
										toggle(span.spanId);
									} else if (e.key === "ArrowRight" && collapsed.has(span.spanId)) {
										e.preventDefault();
										toggle(span.spanId);
									}
								}}
								data-span={span.spanId}
								onFocus={() => {
									onHoverSpan(span.spanId);
								}}
								onBlur={() => {
									onHoverSpan(null);
								}}
								className={cn(
									"min-w-0 truncate text-left font-mono text-[11.5px] hover:underline focus-visible:ring-2 focus-visible:ring-ring",
									spanError(span) && "text-bad-fg",
								)}
							>
								{span.name}
								{collapsed.has(span.spanId) && ` (${children.toString()})`}
								<span className="ml-2 text-[10px] text-fg3">{label(span)}</span>
							</button>
						</div>
						<button
							type="button"
							aria-label={`${span.name}, ${durationLabel(span.durationNanos)}`}
							aria-pressed={selected.spanId === span.spanId}
							className="relative min-w-[100px] flex-1 overflow-hidden text-left font-mono text-[10px] hover:bg-panel2 focus-visible:ring-2 focus-visible:ring-ring"
							onClick={() => {
								onSelect(span);
							}}
							onDoubleClick={() => {
								onZoom(zoomTo(trace, span));
							}}
						>
							{ticks.map((t) => (
								<i
									key={String(t.nanos)}
									className="absolute inset-y-0 border-l border-dashed border-line2"
									style={{ left: `${String(t.left * 100)}%` }}
								/>
							))}
							{off ? (
								<span
									className="absolute inset-y-0 flex items-center px-2 text-fg3"
									style={{ [end < zoom.a ? "left" : "right"]: 0 }}
								>
									{end < zoom.a ? "←" : "→"} {durationLabel(span.durationNanos)}
								</span>
							) : (
								<span
									className={cn(
										"tv-span-bar absolute top-1.5 bottom-1.5 flex items-center truncate rounded-sm px-1 text-foreground",
										selected.spanId === span.spanId && "ring-1 ring-foreground",
									)}
									style={{
										left: `${String(left * 100)}%`,
										width: `${String((right - left) * 100)}%`,
										background: `color-mix(in oklab, ${spanError(span) ? "var(--err)" : color(span)} 35%, var(--bg))`,
									}}
								>
									{durationLabel(span.durationNanos)}
								</span>
							)}
						</button>
					</div>
				);
			})}
		</section>
	);
}
