import { useRef, useState } from "react";
import { cn } from "@/lib/utils";
import { type Zoom } from "../flame";
import { durationLabel } from "../traces";
import { offsetTicks, boundedZoom } from "./plot";
import { Minimap } from "./Minimap";
import type { SetTraceZoom } from "./useTraceZoom";

export function TimeAxis({
	duration,
	zoom,
	onZoom,
	highlight,
}: {
	duration: bigint;
	zoom: Zoom;
	onZoom: SetTraceZoom;
	highlight: string | null;
}) {
	const startRef = useRef<number | null>(null);
	const [brush, setBrush] = useState<Zoom | null>(null);
	const ticks = offsetTicks(duration, zoom);
	return (
		<div data-trace-lane className="min-w-0">
			<Minimap zoom={zoom} onZoom={onZoom} highlight={highlight} />
			<div
				data-trace-axis
				aria-label="Trace time axis"
				title="Drag across the time axis to zoom"
				className="relative h-5 touch-none cursor-col-resize select-none font-mono text-[11px] text-fg3 hover:bg-panel2"
				onPointerDown={(e) => {
					if (e.button !== 0) return;
					e.preventDefault();
					e.stopPropagation();
					const rect = e.currentTarget.getBoundingClientRect();
					startRef.current = (e.clientX - rect.left) / rect.width;
					e.currentTarget.setPointerCapture(e.pointerId);
				}}
				onPointerMove={(e) => {
					if (startRef.current === null) return;
					const rect = e.currentTarget.getBoundingClientRect();
					const end = Math.max(0, Math.min(1, (e.clientX - rect.left) / rect.width));
					setBrush({
						a: Math.min(startRef.current, end),
						b: Math.max(startRef.current, end),
					});
				}}
				onPointerUp={() => {
					if (brush !== null && brush.b - brush.a >= 0.01) {
						const width = zoom.b - zoom.a;
						onZoom(boundedZoom(zoom.a + brush.a * width, (brush.b - brush.a) * width));
					}
					startRef.current = null;
					setBrush(null);
				}}
				onLostPointerCapture={() => {
					startRef.current = null;
					setBrush(null);
				}}
				onPointerCancel={() => {
					startRef.current = null;
					setBrush(null);
				}}
			>
				{ticks.map((t, index) => (
					<span
						key={String(t.nanos)}
						className={cn(
							"pointer-events-none absolute whitespace-nowrap",
							index > 0 && index < ticks.length - 1 && "hidden sm:inline",
							index === 0 ? "" : index === ticks.length - 1 ? "-translate-x-full" : "-translate-x-1/2",
						)}
						style={{ left: `${String(t.left * 100)}%` }}
					>
						{durationLabel(t.nanos)}
					</span>
				))}
				{brush !== null && (
					<span
						aria-hidden
						className="pointer-events-none absolute inset-y-0 border border-info bg-info/15"
						style={{
							left: `${String(brush.a * 100)}%`,
							width: `${String((brush.b - brush.a) * 100)}%`,
						}}
					/>
				)}
			</div>
		</div>
	);
}
