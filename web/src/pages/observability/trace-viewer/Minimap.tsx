import type { KeyboardEvent, PointerEvent } from "react";
import { flameLayout, type Zoom } from "../flame";
import { traceResourceKey } from "../trace-service";
import { useTraceResources } from "./TraceColors";
import type { TraceSummary } from "../traces";
import { cn } from "@/lib/utils";

export function moveWindow(zoom: Zoom, center: number): Zoom {
	const width = zoom.b - zoom.a;
	const a = Math.max(0, Math.min(1 - width, center - width / 2));
	return { a, b: a + width };
}

export function Minimap({
	trace,
	zoom,
	onZoom,
	highlight,
}: {
	trace: TraceSummary;
	zoom: Zoom;
	onZoom: (zoom: Zoom) => void;
	highlight: string | null;
}) {
	const { color: resourceColor } = useTraceResources();
	const { blocks, depth } = flameLayout(trace, { a: 0, b: 1 });
	const move = (e: PointerEvent<HTMLDivElement>) => {
		const rect = e.currentTarget.getBoundingClientRect();
		onZoom(moveWindow(zoom, (e.clientX - rect.left) / rect.width));
	};
	const key = (e: KeyboardEvent<HTMLDivElement>) => {
		if (e.key !== "ArrowLeft" && e.key !== "ArrowRight" && e.key !== "Home" && e.key !== "End") return;
		e.preventDefault();
		const center = (zoom.a + zoom.b) / 2;
		onZoom(
			moveWindow(zoom, e.key === "Home" ? 0 : e.key === "End" ? 1 : center + (e.key === "ArrowLeft" ? -0.05 : 0.05)),
		);
	};
	return (
		<div
			role="slider"
			aria-label="Trace overview"
			aria-valuemin={0}
			aria-valuemax={100}
			aria-valuenow={Math.round(zoom.a * 100)}
			aria-valuetext={`${Math.round(zoom.a * 100).toString()}–${Math.round(zoom.b * 100).toString()}% of trace`}
			tabIndex={0}
			className="tv-mini relative h-12 shrink-0 touch-none overflow-hidden rounded-md border border-line bg-panel focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none"
			onPointerDown={(e) => {
				e.currentTarget.setPointerCapture(e.pointerId);
				move(e);
			}}
			onPointerMove={(e) => {
				if (e.currentTarget.hasPointerCapture(e.pointerId)) move(e);
			}}
			onKeyDown={key}
		>
			{blocks.map((b) => (
				<i
					key={b.span.spanId}
					className={cn(
						"pointer-events-none absolute rounded-[1px]",
						highlight !== null && traceResourceKey(b.span) !== highlight ? "opacity-15" : "opacity-80",
					)}
					style={{
						left: `${String(b.left * 100)}%`,
						width: `${String(b.width * 100)}%`,
						top: `${String((b.depth / depth) * 100)}%`,
						height: `${String(100 / depth)}%`,
						background: resourceColor(b.span),
					}}
				/>
			))}
			<div
				className="pointer-events-none absolute inset-y-0 border-2 border-fg3 bg-panel/15"
				style={{
					left: `${String(zoom.a * 100)}%`,
					width: `${String((zoom.b - zoom.a) * 100)}%`,
				}}
			/>
		</div>
	);
}
