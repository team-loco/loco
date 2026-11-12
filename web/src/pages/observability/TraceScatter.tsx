import { useState, type PointerEvent } from "react";

import { formatMetricTime, timeAxis } from "@/lib/compute-chart";
import { cn } from "@/lib/utils";

import { durationScale, type DurationScale } from "./traces";

export interface ScatterPoint {
	id: string;
	t: number;
	nanos: bigint;
	error: boolean;
}

export interface ScatterZoom {
	from: number;
	to: number;
	minMs: number;
	maxMs: number;
}

interface Brush {
	x0: number;
	y0: number;
	x1: number;
	y1: number;
}

const MIN_BRUSH = 0.01;
const NANOS_PER_MS = 1e6;

function frac(event: PointerEvent<HTMLDivElement>): { x: number; y: number } {
	const box = event.currentTarget.getBoundingClientRect();
	const clamp = (v: number) => Math.min(1, Math.max(0, v));
	return { x: clamp((event.clientX - box.left) / box.width), y: clamp((event.clientY - box.top) / box.height) };
}

function zoomOf(brush: Brush, from: number, to: number, scale: DurationScale): ScatterZoom {
	const span = to - from;
	return {
		from: Math.round(from + Math.min(brush.x0, brush.x1) * span),
		to: Math.round(from + Math.max(brush.x0, brush.x1) * span),
		minMs: scale.msAt(Math.max(brush.y0, brush.y1)),
		maxMs: scale.msAt(Math.min(brush.y0, brush.y1)),
	};
}

export function TraceScatter({
	points,
	from,
	to,
	selectedId,
	onZoom,
}: {
	points: ScatterPoint[];
	from: number;
	to: number;
	selectedId: string;
	onZoom: (zoom: ScatterZoom) => void;
}) {
	const [brush, setBrush] = useState<Brush | null>(null);
	const scale = durationScale(points.map((p) => Number(p.nanos) / NANOS_PER_MS));
	const span = Math.max(1, to - from);
	const xTicks = timeAxis(from, to);

	return (
		<div className="grid grid-cols-[52px_minmax(0,1fr)] grid-rows-[180px_auto] gap-x-2 gap-y-1.5">
			<div aria-hidden className="relative font-mono text-[11px] text-fg3">
				{scale.ticks.map((tick) => (
					<span key={tick.ms} className="absolute right-0 -translate-y-1/2" style={{ top: `${String(tick.y * 100)}%` }}>
						{tick.label}
					</span>
				))}
			</div>
			<div
				role="img"
				aria-label={`Duration of ${String(points.length)} traces over time. Drag across the chart to zoom into a time and duration range.`}
				className="relative cursor-crosshair touch-none border-b border-l border-line2 select-none"
				onPointerDown={(event) => {
					if (event.button !== 0) return;
					event.currentTarget.setPointerCapture(event.pointerId);
					const { x, y } = frac(event);
					setBrush({ x0: x, y0: y, x1: x, y1: y });
				}}
				onPointerMove={(event) => {
					if (brush === null) return;
					const { x, y } = frac(event);
					setBrush({ ...brush, x1: x, y1: y });
				}}
				onPointerUp={() => {
					if (brush === null) return;
					setBrush(null);
					if (Math.abs(brush.x1 - brush.x0) < MIN_BRUSH || Math.abs(brush.y1 - brush.y0) < MIN_BRUSH) return;
					onZoom(zoomOf(brush, from, to, scale));
				}}
				onPointerCancel={() => {
					setBrush(null);
				}}
			>
				{scale.ticks.map((tick) => (
					<span
						key={tick.ms}
						aria-hidden
						className="absolute inset-x-0 border-t border-dashed border-line"
						style={{ top: `${String(tick.y * 100)}%` }}
					/>
				))}
				{points.map((p) => (
					<span
						key={p.id}
						aria-hidden
						className={cn(
							"absolute size-1.5 -translate-1/2 rounded-full",
							p.error ? "bg-err" : "bg-fg3 opacity-60",
							p.id === selectedId && "size-2.5 opacity-100 ring-2 ring-foreground",
						)}
						style={{
							left: `${String(((p.t - from) / span) * 100)}%`,
							top: `${String(scale.yOf(Number(p.nanos) / NANOS_PER_MS) * 100)}%`,
						}}
					/>
				))}
				{brush !== null && (
					<span
						aria-hidden
						className="absolute border border-info bg-info/10"
						style={{
							left: `${String(Math.min(brush.x0, brush.x1) * 100)}%`,
							top: `${String(Math.min(brush.y0, brush.y1) * 100)}%`,
							width: `${String(Math.abs(brush.x1 - brush.x0) * 100)}%`,
							height: `${String(Math.abs(brush.y1 - brush.y0) * 100)}%`,
						}}
					/>
				)}
			</div>
			<span />
			<div aria-hidden className="flex justify-between font-mono text-[11px] text-fg3">
				{xTicks.map((t) => (
					<span key={t}>{formatMetricTime(t, from, to)}</span>
				))}
			</div>
		</div>
	);
}
