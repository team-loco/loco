import type { SetTraceZoom } from "./useTraceZoom";
import { useRef, type KeyboardEvent } from "react";
import { type Zoom } from "../flame";
import { traceResourceKey } from "../trace-service";
import { useTraceResources } from "./TraceColors";
import { cn } from "@/lib/utils";
import { boundedZoom } from "./plot";

function moveWindow(zoom: Zoom, center: number): Zoom {
	const width = zoom.b - zoom.a;
	return boundedZoom(center - width / 2, width);
}

export function Minimap({
	zoom,
	onZoom,
	highlight,
}: {
	zoom: Zoom;
	onZoom: SetTraceZoom;
	highlight: string | null;
}) {
	const { color: resourceColor, layout } = useTraceResources();
	const { blocks, depth } = layout;
	const dragRef = useRef<{
		x: number;
		zoom: Zoom;
		inside: boolean;
		moved: boolean;
	} | null>(null);
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
			className="tv-range-wrap grid grid-rows-[0fr] opacity-0 motion-safe:transition-[grid-template-rows,opacity,margin] motion-safe:duration-200 data-[open=true]:mb-1 data-[open=true]:grid-rows-[1fr] data-[open=true]:opacity-100"
			data-open={zoom.b - zoom.a < 1}
		>
			<div className="min-h-0 overflow-hidden">
				<div
					role="slider"
					aria-label="Trace overview"
					aria-valuemin={0}
					aria-valuemax={100}
					aria-valuenow={Math.round(zoom.a * 100)}
					aria-valuetext={`${Math.round(zoom.a * 100).toString()}–${Math.round(zoom.b * 100).toString()}% of trace`}
					tabIndex={zoom.b - zoom.a < 1 ? 0 : -1}
					aria-hidden={zoom.b - zoom.a >= 1}
					className="tv-mini relative h-6 shrink-0 touch-none overflow-hidden rounded-md border border-line bg-panel focus-visible:ring-2 focus-visible:ring-ring focus-visible:outline-none"
					onPointerDown={(e) => {
						if (e.button !== 0) return;
						e.stopPropagation();
						e.currentTarget.setPointerCapture(e.pointerId);
						const rect = e.currentTarget.getBoundingClientRect();
						const x = (e.clientX - rect.left) / rect.width;
						dragRef.current = {
							x,
							zoom,
							inside: x >= zoom.a && x <= zoom.b,
							moved: false,
						};
					}}
					onPointerMove={(e) => {
						const origin = dragRef.current;
						if (origin === null) return;
						const rect = e.currentTarget.getBoundingClientRect();
						const x = Math.max(0, Math.min(1, (e.clientX - rect.left) / rect.width));
						if (Math.abs(x - origin.x) < 0.003 && !origin.moved) return;
						origin.moved = true;
						if (origin.inside) onZoom(boundedZoom(origin.zoom.a + x - origin.x, origin.zoom.b - origin.zoom.a), true);
						else if (Math.abs(x - origin.x) > 0.004) onZoom({ a: Math.min(origin.x, x), b: Math.max(origin.x, x) }, true);
					}}
					onPointerUp={(e) => {
						const origin = dragRef.current;
						if (origin !== null && !origin.moved && !origin.inside) {
							const rect = e.currentTarget.getBoundingClientRect();
							onZoom(moveWindow(zoom, (e.clientX - rect.left) / rect.width));
						}
						dragRef.current = null;
					}}
					onLostPointerCapture={() => {
						dragRef.current = null;
					}}
					onPointerCancel={() => {
						dragRef.current = null;
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
						className="pointer-events-none absolute inset-y-0 tv-range-window rounded-sm border border-fg2 shadow-[0_0_0_999px_color-mix(in_oklab,var(--bg)_62%,transparent)]"
						style={{
							left: `${String(zoom.a * 100)}%`,
							width: `${String((zoom.b - zoom.a) * 100)}%`,
						}}
					/>
				</div>
			</div>
		</div>
	);
}
