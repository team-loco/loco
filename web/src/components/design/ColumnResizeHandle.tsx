import { useRef } from "react";

export function ColumnResizeHandle({
	label,
	width,
	min,
	max,
	onWidth,
	onDrag,
}: {
	label: string;
	width: number;
	min: number;
	max: number;
	/** Commits a width: on each arrow key and once when a drag ends. */
	onWidth: (value: number) => void;
	/** Previews the width while dragging; null ends the preview. */
	onDrag: (value: number | null) => void;
}) {
	const dragRef = useRef<{ x: number; width: number; value: number } | null>(null);
	const measured = (handle: Element) => handle.parentElement?.getBoundingClientRect().width ?? width;
	const clamp = (value: number) => Math.min(max, Math.max(min, Math.round(value)));
	const resize = (value: number) => {
		onWidth(clamp(value));
	};
	const end = (commit: boolean) => {
		const drag = dragRef.current;
		dragRef.current = null;
		if (drag === null) return;
		onDrag(null);
		if (commit) onWidth(drag.value);
	};
	return (
		<div
			role="separator"
			aria-orientation="vertical"
			aria-label={`Resize ${label}`}
			aria-valuenow={width}
			aria-valuemin={min}
			aria-valuemax={max}
			tabIndex={0}
			onKeyDown={(event) => {
				if (event.key === "ArrowLeft" || event.key === "ArrowRight") {
					event.preventDefault();
					resize(measured(event.currentTarget) + (event.key === "ArrowLeft" ? -16 : 16));
				}
				if (event.key === "Escape" && dragRef.current !== null) {
					end(false);
				}
			}}
			onPointerDown={(event) => {
				event.currentTarget.setPointerCapture(event.pointerId);
				const start = measured(event.currentTarget);
				dragRef.current = { x: event.clientX, width: start, value: clamp(start) };
			}}
			onPointerMove={(event) => {
				const drag = dragRef.current;
				if (drag === null) return;
				drag.value = clamp(drag.width + event.clientX - drag.x);
				onDrag(drag.value);
			}}
			onPointerUp={() => {
				end(true);
			}}
			onPointerCancel={() => {
				end(false);
			}}
			onLostPointerCapture={() => {
				end(true);
			}}
			className="absolute top-2 right-0 bottom-2 w-2 cursor-col-resize touch-none rounded-sm outline-none after:absolute after:inset-y-0 after:right-[3px] after:w-px after:bg-line2 after:opacity-0 after:transition-opacity after:duration-120 in-[[data-slot=table-header]:hover]:after:opacity-100 hover:after:bg-fg3 hover:after:opacity-100 focus-visible:after:bg-foreground focus-visible:after:opacity-100"
		/>
	);
}
